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

// Outcome says what happened to a nudge somebody asked for.
//
// Three answers rather than a bool, because two of the three failures are different
// afternoons and a caller that cannot tell them apart has to guess at the message. "Nothing
// to send" when the truth was "nothing reached your phone" is the kind of excuse that sends
// somebody looking in the wrong place.
//
// An alias rather than a defined type, so internal/api can declare its Scheduler interface
// in terms of `string` and not import this package — which is what keeps the dependency
// running one way.
type Outcome = string

const (
	// Sent means at least one device took it.
	Sent Outcome = "sent"
	// NothingToSend means the pool was empty: everything is finished or silenced.
	NothingToSend Outcome = "nothing"
	// Undelivered means something was chosen and no push service would take it.
	Undelivered Outcome = "undelivered"
)

// sweepEvery is how often expired sessions are cleared.
const sweepEvery = 10 * time.Minute

// Scheduler decides when somebody is owed a nudge, and sends it.
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
	tick := time.NewTicker(rhythm.Tick)
	defer tick.Stop()
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()

	// Once at startup, so a process that has just been restarted does not wait a tick to
	// notice somebody is overdue.
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

// pass is one look at the clock.
//
// There is no plan to consult and nothing to claim. Whether somebody is owed a nudge is a
// question about their waking hours and when they were last nudged, and both are already
// known — so a restart mid-day costs nothing, a change to the rhythm applies at the very
// next tick, and an instance that was down for three hours sends one nudge rather than
// discovering a queue of them.
func (s *Scheduler) pass(ctx context.Context) {
	now := s.store.Now()

	people, err := s.store.PrincipalsWithDevices(ctx)
	if err != nil {
		s.log.Error("scheduler could not list people", "err", err)
		return
	}
	for _, id := range people {
		if err := s.consider(ctx, id, now); err != nil {
			s.log.Error("could not decide whether a nudge is due", "principal", id, "err", err)
		}
	}
}

// consider is the whole of the scheduling decision for one person.
//
// Three states and no plan.
//
//  1. A nudge is waiting and its moment has come — send it.
//  2. A nudge is waiting and its moment has not — leave it alone. Deciding again while one
//     stands would move it, and a nudge that keeps being rescheduled never arrives.
//  3. Nothing is waiting — work out whether one is owed, and if so schedule it a random
//     moment from now rather than firing here. The loop only wakes on a tick, so firing on
//     the spot would put every nudge on a five-minute mark.
func (s *Scheduler) consider(ctx context.Context, principalID string, now time.Time) error {
	rh, err := s.store.Rhythm(ctx, principalID)
	if err != nil {
		return err
	}

	scheduled, err := s.store.ScheduledNudge(ctx, principalID)
	if err != nil {
		return err
	}
	if !scheduled.IsZero() {
		if scheduled.After(now) {
			return nil
		}
		// Its moment came. Claiming is the delete, so two overlapping ticks cannot both
		// send the same one.
		got, err := s.store.ClaimScheduledNudge(ctx, principalID, now)
		if err != nil || !got {
			return err
		}
		// Scheduled while awake and arrived after bedtime — an evening that ran out before
		// the nudge did. Dropped rather than sent late.
		if !rhythm.Awake(rh, now) {
			s.log.Debug("a scheduled nudge came due after bedtime", "principal", principalID)
			return nil
		}
		_, _, err = s.deliver(ctx, principalID, store.RespectFloor)
		return err
	}

	last, err := s.store.LastNudgeAt(ctx, principalID)
	if err != nil {
		return err
	}
	if !rhythm.Due(rh, rhythm.Since(rh, last, now), now) {
		return nil
	}
	return s.store.ScheduleNudge(ctx, principalID, now.Add(rhythm.Delay(rh)))
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

	// The worker cannot read a rhythm, so whether this one is silent rides with it.
	rh, err := s.store.Rhythm(ctx, principalID)
	if err != nil {
		return NothingToSend, 0, err
	}
	payload, err := json.Marshal(map[string]any{
		"nudge_id": nudgeID,
		"text":     chosen.Text,
		"silent":   rh.Silent,
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
// because this runs inside a pass that everybody else's turn is waiting behind.
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
}
