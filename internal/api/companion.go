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
// Best effort and never fatal: out-of-date advice is not a reason to refuse somebody's edit.
// Called from every write that could change what the companion would say, and from no other —
// which is why nudging a reminder is not one of them.
// adviceForgotten drops what was said about a reminder deleted outright. Never on a binning: a
// binned reminder can come back, and what was said about it is still true.
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

// adviceStatus is how the last round of questions went.
//
// No counts, per docs/api_design.md: "some of your reminders" says what somebody needs without
// putting a number that goes up on a settings screen. The states are derived here rather than
// assembled from timestamps by each client.
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

// listAdvice is what the companion currently thinks, as it is actually stored.
//
// A window onto the weighting rather than a setting: a wrong answer is otherwise something
// somebody can feel and never see. Only the open list and only the current shape, because that
// is exactly what the draw reads.
func (s *Server) listAdvice(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	reminders, err := s.store.Reminders(r.Context(), p.ID, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	ids := make([]string, len(reminders))
	for i, rem := range reminders {
		ids[i] = rem.ID
	}
	advice, err := s.store.AdviceFor(r.Context(), ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(reminders))
	for _, rem := range reminders {
		row := map[string]any{
			"id":      rem.ID,
			"text":    rem.Text,
			"advised": false,
		}
		if a, ok := advice[rem.ID]; ok {
			row["advised"] = true
			row["categories"] = a.Categories
			row["exclusive"] = a.Exclusive
			// Sent whole, and only when it is the shape the weighting reads. A curve the
			// program would ignore is not something to draw.
			if a.Curve.Valid() {
				row["curve"] = a.Curve
			} else if a.Shape != "" {
				// What arrived instead, so the screen can name it. "7x24" says the model
				// answered by the hour; "obj:6" says it left a day out.
				row["shape"] = a.Shape
			}
			row["advised_at"] = unixOrNil(a.AdvisedAt)
		}
		out = append(out, row)
	}

	// The state rides along, so a screen watching for a fresh answer has one thing to watch
	// rather than two queries it has to reconcile. `stale` is the whole of it: true means an
	// answer is still owed, and it going false is the moment the drawing below changed.
	state, err := s.store.Advice(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// An envelope rather than a bare array, per docs/api_design.md, which is what lets a field
	// be added later.
	writeJSON(w, http.StatusOK, map[string]any{
		"reminders":  out,
		"stale":      state.Stale,
		"advised_at": unixOrNil(state.AdvisedAt),
		"error":      state.Error,
		// So the screen can lay a day out without knowing the shape by heart, and cannot
		// disagree with the server about it.
		"days":    store.Days,
		"windows": store.Windows,
	})
}

// refreshAdvice asks the companion again, and waits for the answer.
//
// It blocks for as long as the model takes, which is long by the standards of everything else
// here and is a deliberate press with somebody watching it. Answering before the work is done
// would make them judge a change they cannot see.
//
// Rate limited: it spends from a quota with fifty a day in it, and the screen's own twenty
// seconds is not a ceiling on a screen that is not the one being used.
func (s *Server) refreshAdvice(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.adviceLimit.allow(p.ID) {
		writeError(w, http.StatusTooManyRequests, "that was just asked; give it a moment")
		return
	}
	if s.advise == nil {
		writeError(w, http.StatusServiceUnavailable, "nothing is asking on your behalf")
		return
	}

	// Marked first, because Look declines when nothing has changed — which is right for the
	// loop and wrong for a press that means "ask anyway".
	if err := s.store.MarkAdviceStale(r.Context(), p.ID); err != nil {
		s.fail(w, r, err)
		return
	}

	if err := s.advise.Look(r.Context(), p.ID); err != nil {
		// 502 rather than 500, for the reason docs/mail.md gives about a refused send:
		// everything on this side worked and something upstream did not.
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// The answer itself, so the screen redraws from the response rather than asking again.
	s.listAdvice(w, r)
}

// testCompanion puts one question to the model and reports what answered.
//
// Against what is in the form, not what was last saved: a button beside a field somebody has
// just corrected has to mean that correction. It reconciles under Save's rule — an empty key
// means the stored one — so what was tried is what saving would store.
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

	// The instance's way out, which is an administrator's setting rather than this account's.
	// Read here so that a test press proves the path the companion will actually take.
	via, err := s.store.Proxy(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}

	res, err := openrouter.Check(r.Context(), set, via)
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
