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
		if _, err := s.deliver(ctx, slot.PrincipalID); err != nil {
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
// It goes through exactly the same path as a scheduled nudge — same selection, same
// encryption, same log — because a test button that takes a shortcut tests the shortcut.
func (s *Scheduler) NudgeNow(ctx context.Context, principalID string) (bool, error) {
	return s.deliver(ctx, principalID)
}

// deliver chooses a reminder and sends it, and reports whether anything went out.
func (s *Scheduler) deliver(ctx context.Context, principalID string) (bool, error) {
	now := s.store.Now()

	candidates, err := s.store.Candidates(ctx, principalID, now)
	if err != nil {
		return false, err
	}
	last, err := s.store.LastNudgedReminder(ctx, principalID)
	if err != nil {
		return false, err
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
		return false, nil
	}

	devices, err := s.store.Devices(ctx, principalID)
	if err != nil {
		return false, err
	}
	if len(devices) == 0 {
		return false, nil
	}

	payload, err := json.Marshal(map[string]string{
		"nudge_id": nudgeID,
		"text":     chosen.Text,
	})
	if err != nil {
		return false, err
	}

	delivered := s.fanOut(ctx, devices, payload)
	if !delivered {
		// Nothing reached a push service, so nothing is recorded: the reminder keeps its
		// place in the pool rather than spending its floor on a notification nobody got.
		s.log.Warn("nudge reached nobody", "principal", principalID, "devices", len(devices))
		return false, nil
	}

	if _, err := s.store.RecordNudge(ctx, nudgeID, principalID, chosen.ID); err != nil {
		return true, err
	}
	s.log.Info("nudge sent", "principal", principalID, "nudge", nudgeID, "reminder", chosen.ID)
	return true, nil
}

// fanOut sends to every one of a person's devices at once and reports whether any of them
// took it.
//
// Concurrently because a phone whose push service is slow must not delay the laptop's, and
// because this runs inside a scheduler pass that other people's slots are waiting behind.
func (s *Scheduler) fanOut(ctx context.Context, devices []store.Device, payload []byte) bool {
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok bool
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
				ok = true
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
