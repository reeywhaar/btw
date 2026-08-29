// Package nudge is the scheduler: the part that reads a clock and talks to the network.
//
// The two decisions it orchestrates are pure and live elsewhere — internal/rhythm says
// when, internal/pick says what — and keeping them out of here is what makes them testable
// against a fixed seed rather than against an afternoon.
package nudge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"btw/internal/pick"
	"btw/internal/rhythm"
	"btw/internal/store"
	"btw/internal/webpush"
)

// Tick is how often the scheduler looks. A minute is the resolution a slot is planned at,
// so looking more often would find nothing new.
//
// A ticker rather than a chain that reschedules itself after each pass: the spacing is
// identical, but a chain has no owner — lose the goroutine between finishing one pass and
// queueing the next and the work stops with nothing to notice.
const Tick = time.Minute

// sweepEvery is how often expired sessions and stale slots are cleared.
const sweepEvery = 10 * time.Minute

// Outcome says what happened to a nudge somebody asked for.
//
// Three answers rather than a bool, because two of the three failures are different
// afternoons and a caller that cannot tell them apart has to guess at the message. "Nothing
// to send" when the truth was "nothing reached your phone" is the kind of excuse that sends
// somebody looking in the wrong place.
// An alias rather than a defined type, so internal/api can declare its one-method Nudger
// interface in terms of `string` and not have to import this package — which is what keeps
// the dependency running one way.
type Outcome = string

const (
	// Sent means at least one device took it.
	Sent Outcome = "sent"
	// NothingToSend means the pool was empty: everything is finished or silenced.
	NothingToSend Outcome = "nothing"
	// Undelivered means something was chosen and no push service would take it.
	Undelivered Outcome = "undelivered"
)

// Scheduler plans days, fires slots, and sends.
type Scheduler struct {
	store *store.Store
	push  *webpush.Sender
	log   *slog.Logger
}

func New(st *store.Store, push *webpush.Sender, log *slog.Logger) *Scheduler {
	return &Scheduler{store: st, push: push, log: log}
}

// Run ticks until the context is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	tick := time.NewTicker(Tick)
	defer tick.Stop()
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()

	// Once at startup, so a process that has just been restarted plans the day it woke up
	// into rather than waiting a minute to notice.
	s.pass(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.pass(ctx)
		case <-sweep.C:
			s.sweep(ctx)
		}
	}
}

// pass is one look at the clock: plan whatever is unplanned, fire whatever is due.
func (s *Scheduler) pass(ctx context.Context) {
	now := s.store.Now()

	people, err := s.store.PrincipalsWithDevices(ctx)
	if err != nil {
		s.log.Error("scheduler could not list people", "err", err)
		return
	}
	for _, id := range people {
		if err := s.plan(ctx, id, now); err != nil {
			s.log.Error("could not plan a day", "principal", id, "err", err)
		}
	}

	due, err := s.store.DueSlots(ctx, now, rhythm.Grace)
	if err != nil {
		s.log.Error("scheduler could not read due slots", "err", err)
		return
	}
	for _, slot := range due {
		// Claimed before anything is sent, so two overlapping passes cannot both fire one
		// slot. The claim is the UPDATE itself rather than a note made after deciding to
		// claim.
		got, err := s.store.ClaimSlot(ctx, slot, now)
		if err != nil {
			s.log.Error("could not claim a slot", "principal", slot.PrincipalID, "err", err)
			continue
		}
		if !got {
			continue
		}
		if _, _, err := s.deliver(ctx, slot.PrincipalID, store.RespectFloor); err != nil {
			s.log.Error("could not deliver a nudge", "principal", slot.PrincipalID, "err", err)
		}
	}
}

// plan makes a day's slots if that day has none. Lazy rather than a cron per timezone:
// nothing is planned for somebody with nowhere to deliver, and nothing has to know when
// midnight is in forty places.
func (s *Scheduler) plan(ctx context.Context, principalID string, now time.Time) error {
	rh, err := s.store.Rhythm(ctx, principalID)
	if err != nil {
		return err
	}
	date := rhythm.LocalDate(rh, now)
	planned, err := s.store.HasPlan(ctx, principalID, date)
	if err != nil || planned {
		return err
	}
	at, err := rhythm.PlanDay(rh, date, principalID)
	if err != nil {
		return err
	}
	if len(at) == 0 {
		return nil
	}
	if err := s.store.PlanDay(ctx, principalID, date, at); err != nil {
		return err
	}
	s.log.Debug("planned a day", "principal", principalID, "date", date, "slots", len(at))
	return nil
}

// NudgeNow sends one immediately, for the button that proves the chain.
//
// The same path as a scheduled nudge — same selection, same encryption, same log — because
// a test button that takes a shortcut tests the shortcut. It differs in one rule, and only
// one: a reminder's own interval does not apply. Somebody pressing this has asked for a
// nudge, and "that was raised too recently" refuses a request nobody made on their behalf.
func (s *Scheduler) NudgeNow(ctx context.Context, principalID string) (Outcome, int, error) {
	return s.deliver(ctx, principalID, store.IgnoreFloor)
}

// deliver chooses a reminder and sends it, and reports whether anything went out.
// The int is how many devices took it. Reported rather than kept because "it sent one push
// and you saw two notifications" and "it sent two pushes" are different faults, and without
// the number there is no way to tell them apart from the outside.
func (s *Scheduler) deliver(ctx context.Context, principalID string, floor store.Floor) (Outcome, int, error) {
	now := s.store.Now()

	candidates, err := s.store.Candidates(ctx, principalID, now, floor)
	if err != nil {
		return NothingToSend, 0, err
	}
	last, err := s.store.LastNudgedReminder(ctx, principalID)
	if err != nil {
		return NothingToSend, 0, err
	}

	// The id is minted before the choice because it seeds it, and because it has to travel
	// inside the payload — the notification's buttons post back to it.
	nudgeID := store.NewNudgeID()

	chosen, ok := pick.Pick(candidates, now, last, nudgeID)
	if !ok {
		// Nothing eligible sends nothing at all. Reaching for the next-least-ineligible
		// reminder, or repeating this morning's, is how a notification channel gets turned
		// off for good by somebody who was otherwise happy with it.
		s.log.Debug("nothing to nudge with", "principal", principalID, "candidates", len(candidates))
		return NothingToSend, 0, nil
	}

	devices, err := s.store.Devices(ctx, principalID)
	if err != nil {
		return NothingToSend, 0, err
	}
	if len(devices) == 0 {
		return Undelivered, 0, nil
	}

	payload, err := json.Marshal(map[string]string{
		"nudge_id": nudgeID,
		"text":     chosen.Text,
	})
	if err != nil {
		return NothingToSend, 0, err
	}

	delivered := s.fanOut(ctx, devices, payload)
	if delivered == 0 {
		// Nothing reached a push service, so nothing is recorded: the reminder keeps its
		// place in the pool rather than spending its floor on a notification nobody got.
		s.log.Warn("nudge reached nobody", "principal", principalID, "devices", len(devices))
		return Undelivered, 0, nil
	}

	if _, err := s.store.RecordNudge(ctx, nudgeID, principalID, chosen.ID); err != nil {
		return Sent, delivered, err
	}
	s.log.Info("nudge sent", "principal", principalID, "nudge", nudgeID, "reminder", chosen.ID,
		"devices", len(devices), "delivered", delivered)
	return Sent, delivered, nil
}

// fanOut sends to every one of a person's devices at once and reports whether any of them
// took it.
//
// Concurrently because a phone whose push service is slow must not delay the laptop's, and
// because this runs inside a scheduler pass that other people's slots are waiting behind.
func (s *Scheduler) fanOut(ctx context.Context, devices []store.Device, payload []byte) int {
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for _, d := range devices {
		wg.Add(1)
		go func(d store.Device) {
			defer wg.Done()
			err := s.push.Send(ctx, webpush.Subscription{
				Endpoint: d.Endpoint,
				P256dh:   d.P256dh,
				Auth:     d.Auth,
			}, payload)

			switch {
			case err == nil:
				s.store.DeviceDelivered(ctx, d.ID)
				mu.Lock()
				ok++
				mu.Unlock()
			case webpush.Gone(err):
				// A browser that has been reinstalled leaves an endpoint that refuses
				// forever. Deleting rather than counting is what keeps a device list from
				// filling with dead rows nobody can explain.
				s.log.Info("device is gone", "device", d.ID, "err", err)
				s.store.DeleteDeviceByID(ctx, d.ID)
			default:
				var f *webpush.Failure
				reason := "unknown"
				if errors.As(err, &f) {
					reason = f.Reason
				}
				s.log.Warn("push failed", "device", d.ID, "reason", reason, "err", err)
				s.store.DeviceFailed(ctx, d.ID, err.Error())
			}
		}(d)
	}
	wg.Wait()
	return ok
}

func (s *Scheduler) sweep(ctx context.Context) {
	if n, err := s.store.SweepSessions(ctx); err != nil {
		s.log.Error("could not sweep sessions", "err", err)
	} else if n > 0 {
		s.log.Debug("swept sessions", "count", n)
	}
	// Anything past the grace window is marked fired without sending, so the due query
	// stays small and a restart does not reconsider last week.
	if n, err := s.store.ExpireSlots(ctx, s.store.Now().Add(-rhythm.Grace)); err != nil {
		s.log.Error("could not expire slots", "err", err)
	} else if n > 0 {
		s.log.Info("slots missed", "count", n)
	}
}
