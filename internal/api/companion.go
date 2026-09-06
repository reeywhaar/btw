package api

import (
	"net/http"
	"strings"

	"btw/internal/openrouter"
	"btw/internal/store"
)

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
	set, err := s.store.Companion(r.Context(), principal(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, companionJSON(set))
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
	s.getCompanion(w, r)
}

func (s *Server) deleteCompanion(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.store.ClearCompanion(r.Context(), p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("companion forgotten", "by", p.Username)
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
