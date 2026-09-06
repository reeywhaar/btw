package api

import (
	"net/http"
	"strings"
	"time"

	"btw/internal/openrouter"
	"btw/internal/store"
)

// adviceStale tells the companion loop that something changed under it.
//
// Best effort and never fatal, exactly like the scheduled nudge a rhythm change drops: failing
// to mark means advice stays a little out of date, and that is not a reason to refuse
// somebody's edit. It is called from every write that could change what the companion would
// say — a reminder written, described, ended, revived or deleted, an `about` rewritten, a
// rhythm moved — and never from one that could not, which is why nudging a reminder does not
// appear in that list.
// adviceForgotten drops what was said about a reminder that has been deleted outright.
//
// Only a delete, never a done: a finished reminder can be revived, and what the companion said
// about it is still true. Best effort like the marking above — a row left behind is read by
// nothing, since the weighting only ever asks about reminders that still exist.
func (s *Server) adviceForgotten(r *http.Request, reminderID string) {
	if err := s.store.ForgetAdvice(r.Context(), reminderID); err != nil {
		s.log.Error("could not forget advice", "reminder", reminderID, "err", err)
	}
}

func (s *Server) adviceStale(r *http.Request, principalID string) {
	if err := s.store.MarkAdviceStale(r.Context(), principalID); err != nil {
		s.log.Error("could not mark advice stale", "principal", principalID, "err", err)
	}
}

// companionJSON is the companion as it goes out, which is everything except the key.
//
// A stored key is never sent back, for the reason docs/mail.md gives about the relay's
// password: it would be readable by anything that can read a response for no gain, since the
// form does not need it to save a change. `key_set` is what the interface actually needs.
func companionJSON(set openrouter.Settings) map[string]any {
	return map[string]any{
		"configured": set.Configured(),
		"model":      set.Model,
		"key_set":    set.APIKey != "",
		"about":      set.About,
		// So the form can offer the default as a placeholder rather than hard-coding a model
		// name in two languages, where the two would drift.
		"default_model": openrouter.DefaultModel,
		"about_limit":   store.AboutLimit,
	}
}

func (s *Server) getCompanion(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	set, err := s.store.Companion(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	body := companionJSON(set)
	// Only for a configured companion. Reporting on the advice of an account that has no key
	// would be reporting on a loop that never runs.
	if set.Configured() {
		advice, err := s.adviceStatus(r, p.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		body["advice"] = advice
	}
	writeJSON(w, http.StatusOK, body)
}

// adviceStatus is how the last round of questions went, as the interface needs it.
//
// **No counts.** docs/api_design.md forbids one in a response body, and the reason applies
// here as much as anywhere: a count in a payload is a count somebody renders, and "5 of 7
// reminders" is a number that goes up. What somebody actually needs to know is whether the
// companion has an opinion about everything or only some of it, and "some" says that without
// putting their workload on a settings screen.
//
// The four states are derived here rather than left to the client to assemble out of
// timestamps, so that "failed" means the same thing everywhere it is shown.
func (s *Server) adviceStatus(r *http.Request, principalID string) (map[string]any, error) {
	state, err := s.store.Advice(r.Context(), principalID)
	if err != nil {
		return nil, err
	}
	open, answered, err := s.store.AdviceCoverage(r.Context(), principalID)
	if err != nil {
		return nil, err
	}

	status := "none"
	switch {
	// A failure is reported over any coverage, because the coverage is what the *last
	// successful* round left behind and saying "answered for all of them" while the key is
	// rejected is the exact confusion this is meant to end.
	case state.Limited:
		// Not "failed". A quota is not a mistake and wants nothing done about it, and showing
		// it the way a rejected key is shown sends somebody to check a key that is fine.
		status = "limited"
	case state.Error != "":
		status = "failed"
	case state.AdvisedAt.IsZero():
		status = "none"
	case open == 0 || answered >= open:
		status = "all"
	case answered > 0:
		status = "some"
	default:
		status = "none"
	}

	return map[string]any{
		"status": status,
		// Unix seconds, per the API's rule about timestamps: rendering "four minutes ago" in
		// a reader's own zone is the browser's job.
		"advised_at":   unixOrNil(state.AdvisedAt),
		"attempted_at": unixOrNil(state.AttemptedAt),
		"error":        state.Error,
		// Whether something has changed since the last answer, so the interface can say a
		// fresh look is coming rather than presenting stale advice as current.
		"stale": state.Stale,
	}, nil
}

func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func (s *Server) putCompanion(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var req struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
		About  string `json:"about"`
	}
	if !decode(w, r, &req) {
		return
	}

	set := openrouter.Settings{APIKey: req.APIKey, Model: req.Model, About: req.About}
	// An empty key keeps the one already stored, which is what lets somebody change the model
	// or rewrite their description without retyping a credential the form was never given.
	if set.APIKey == "" {
		current, err := s.store.Companion(r.Context(), p.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		set.APIKey = current.APIKey
	}

	if err := s.store.SetCompanion(r.Context(), p.ID, set); err != nil {
		s.fail(w, r, err)
		return
	}
	// The model and never the key, and no part of about either: what somebody wrote about
	// their own life is not a thing to leave in a log an operator reads.
	s.log.Info("companion configured", "model", set.Model, "by", p.Username)
	// The `about` may have been rewritten, which changes every answer about this person.
	s.adviceStale(r, p.ID)
	s.getCompanion(w, r)
}

func (s *Server) deleteCompanion(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.store.ClearCompanion(r.Context(), p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("companion forgotten", "by", p.Username)
	// Marked stale rather than cleared. The advice already given stays and keeps working —
	// it was true when it was written — and if a key is added again there is nothing to
	// rebuild. Nothing asks on behalf of an account with no companion, so the flag simply
	// waits.
	s.adviceStale(r, p.ID)
	w.WriteHeader(http.StatusNoContent)
}

// testCompanion puts one question to the model and reports what answered.
//
// Against what is in the form, not against what was last saved — unlike the relay's test
// send, which has no key to reconcile. The button lives inside the dialog, and a button beside
// a field somebody has just corrected has to mean that correction, or pressing it teaches them
// the wrong thing about the value they are looking at.
//
// It reconciles under the same rule Save follows: an empty key means the stored one. So what
// was tried is what saving would store, which is what keeps the shortcut honest.
func (s *Server) testCompanion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	if !decode(w, r, &req) {
		return
	}

	stored, err := s.store.Companion(r.Context(), principal(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	set := openrouter.Settings{
		APIKey: strings.TrimSpace(req.APIKey),
		Model:  strings.TrimSpace(req.Model),
	}
	if set.APIKey == "" {
		set.APIKey = stored.APIKey
	}
	if set.Model == "" {
		// The stored model, or the default when there is no row yet — so the first press,
		// made before anything has ever been saved, asks the model the form is offering
		// rather than refusing for want of a field nobody filled in.
		set.Model = stored.Model
	}
	if set.Model == "" {
		set.Model = openrouter.DefaultModel
	}

	if !set.Configured() {
		writeError(w, http.StatusBadRequest, "add a key first")
		return
	}

	res, err := openrouter.Check(r.Context(), set)
	if err != nil {
		// 502 rather than 500, for the reason docs/mail.md gives about a refused send:
		// everything on this side worked and something upstream did not, and a 500 sends
		// somebody through the wrong logs. The gateway's own words go with it.
		s.log.Warn("companion test failed", "model", set.Model, "err", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// What actually answered, which is not always what was asked for — OpenRouter falls
		// back between providers, and a slug can resolve to a variant.
		"model":  res.Model,
		"tokens": res.Tokens,
	})
}
