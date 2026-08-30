package api

import (
	"net/http"

	"btw/internal/ids"
	"btw/internal/store"
)

func reminderJSON(r store.Reminder) map[string]any {
	out := map[string]any{
		"id":   r.ID,
		"text": r.Text,
		// What the sentence could not hold. Never sent in a push — a lock screen is the one
		// place this deliberately does not appear.
		"note":       r.Note,
		"created_at": r.CreatedAt.Unix(),
		"done_at":    nil,
	}
	if r.Done() {
		out["done_at"] = r.DoneAt.Unix()
	}
	// last_nudged_at is deliberately absent. It is how the selection works, not something
	// a person is meant to reason about — and showing it invites exactly the arithmetic
	// this product exists to avoid.
	return out
}

func (s *Server) listReminders(w http.ResponseWriter, r *http.Request) {
	// Ended ones are a separate ask rather than a filter on one list, because they are two
	// different screens and the ended list is the one nobody looks at.
	done := r.URL.Query().Get("done") == "true"

	list, err := s.store.Reminders(r.Context(), principal(r).ID, done)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, rem := range list {
		out = append(out, reminderJSON(rem))
	}
	// Never a bare array and never a count: the envelope is what lets a field be added
	// later, and a "total" is the number this product exists not to show.
	writeJSON(w, http.StatusOK, map[string]any{"reminders": out})
}

func (s *Server) createReminder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	// One field, because that is the entire path to a reminder existing. Everything else
	// has a default that is deliberately invisible.
	rem, err := s.store.CreateReminder(r.Context(), principal(r).ID, req.Text)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, reminderJSON(rem))
}

func (s *Server) updateReminder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.Valid(ids.Reminder, id) {
		writeError(w, http.StatusBadRequest, "that is not a reminder")
		return
	}
	p := principal(r)

	current, err := s.store.Reminder(r.Context(), p.ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Pointers, so absent leaves a field alone and empty clears it — which is how a
	// description gets deleted without also having to retype the sentence.
	var req struct {
		Text *string `json:"text"`
		Note *string `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	text, note := current.Text, current.Note
	if req.Text != nil {
		text = *req.Text
	}
	if req.Note != nil {
		note = *req.Note
	}

	updated, err := s.store.UpdateReminder(r.Context(), p.ID, id, text, note)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reminderJSON(updated))
}

// endReminder is what both buttons reach. Done and Drop end a reminder identically; which
// was pressed is recorded on the nudge that was answered, when there was one.
func (s *Server) endReminder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.Valid(ids.Reminder, id) {
		writeError(w, http.StatusBadRequest, "that is not a reminder")
		return
	}
	if err := s.store.EndReminder(r.Context(), principal(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reviveReminder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.Valid(ids.Reminder, id) {
		writeError(w, http.StatusBadRequest, "that is not a reminder")
		return
	}
	if err := s.store.ReviveReminder(r.Context(), principal(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteReminder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.Valid(ids.Reminder, id) {
		writeError(w, http.StatusBadRequest, "that is not a reminder")
		return
	}
	if err := s.store.DeleteReminder(r.Context(), principal(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
