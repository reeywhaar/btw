package api

import (
	"net/http"
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
		"min_gap":        rh.MinGap,
		// What the window can hold, so the interface can bound its own control rather
		// than offering a number the save will refuse.
		"max_budget": rh.MaxBudgetForWindow(),
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
		MinGap        *int    `json:"min_gap"`
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
	if req.MinGap != nil {
		next.MinGap = *req.MinGap
	}
	if err := s.store.SetRhythm(r.Context(), next); err != nil {
		s.fail(w, r, err)
		return
	}
	// Today's plan was drawn under the old rhythm and is not redrawn. Changing the window
	// at noon and having the afternoon's nudges jump would be a surprise in the wrong
	// direction; the new rhythm is what tomorrow is planned from.
	s.getRhythm(w, r)
}
