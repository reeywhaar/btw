package api

import (
	"net/http"

	"btw/internal/store"
)

func (s *Server) getRhythm(w http.ResponseWriter, r *http.Request) {
	rh, err := s.store.Rhythm(r.Context(), principal(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timezone":       rh.Timezone,
		"window_enabled": rh.WindowEnabled,
		"wake_minute":    rh.WakeMinute,
		"sleep_minute":   rh.SleepMinute,
		"budget":         rh.Budget,
		"silent":         rh.Silent,
		// The most anybody may ask for. A plain number: the interval is the waking day
		// divided by the budget and is floored at one tick, so nothing else bounds it.
		"max_budget": store.MaxBudget,
	})
	// No next_nudge_at, and there never will be one. A person who can see that the next
	// nudge is at 14:32 is a person waiting for 14:32, and the surprise is the mechanism.
}

func (s *Server) patchRhythm(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	current, err := s.store.Rhythm(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Pointers, so absent and zero are different: a budget of 0 is somebody switching
	// nudges off, and a missing budget is a request about something else entirely.
	var req struct {
		Timezone      *string `json:"timezone"`
		WindowEnabled *bool   `json:"window_enabled"`
		WakeMinute    *int    `json:"wake_minute"`
		SleepMinute   *int    `json:"sleep_minute"`
		Budget        *int    `json:"budget"`
		Silent        *bool   `json:"silent"`
	}
	if !decode(w, r, &req) {
		return
	}
	next := current
	if req.Timezone != nil {
		next.Timezone = *req.Timezone
	}
	if req.WindowEnabled != nil {
		next.WindowEnabled = *req.WindowEnabled
	}
	if req.WakeMinute != nil {
		next.WakeMinute = *req.WakeMinute
	}
	if req.SleepMinute != nil {
		next.SleepMinute = *req.SleepMinute
	}
	if req.Budget != nil {
		next.Budget = *req.Budget
	}
	if req.Silent != nil {
		next.Silent = *req.Silent
	}
	if err := s.store.SetRhythm(r.Context(), next); err != nil {
		s.fail(w, r, err)
		return
	}

	// Anything already scheduled was decided under the old answer — at an interval that has
	// changed, or for a moment that may now be the middle of the night. Dropping it lets the
	// next tick work the whole thing out again, which is the only thing a rhythm change has
	// to do now that there is no plan to redraw.
	//
	// Never fatal: the rhythm is saved either way, and the worst a failure costs is one
	// nudge arriving on the old schedule.
	if err := s.store.ClearScheduledNudge(r.Context(), p.ID); err != nil {
		s.log.Error("could not clear the scheduled nudge", "principal", p.ID, "err", err)
	}
	s.getRhythm(w, r)
}
