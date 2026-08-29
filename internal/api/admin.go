package api

import (
	"net/http"
	"strings"

	"btw/internal/mail"
	"btw/internal/store"
)

// requireAdmin is the second of the program's two authorisation decisions, and like the
// first it is made where the route is registered rather than inside a handler.
func (s *Server) requireAdmin(h http.HandlerFunc) http.Handler {
	// Wraps requireSession rather than sitting beside it, so an admin route cannot be
	// registered with the role check and without the session check.
	return s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Role != store.RoleAdmin {
			// 403 rather than 404: an administrator route is not a secret — the interface
			// simply does not offer it — and "you are signed in, and this is not yours" is
			// the honest answer.
			writeError(w, http.StatusForbidden, "that is for administrators")
			return
		}
		h(w, r)
	})
}

// relayJSON is the relay as it goes out, which is everything except the password.
//
// A stored password is never sent back. It would be readable by anything that can read a
// response — an extension, a proxy, a screen — for no gain, since the form does not need it
// to save a change. `password_set` is what the interface actually needs to know.
func relayJSON(set mail.Settings, configured bool) map[string]any {
	return map[string]any{
		"configured":   configured,
		"host":         set.Host,
		"port":         set.Port,
		"tls":          string(set.TLS),
		"username":     set.Username,
		"password_set": set.Password != "",
		"from_address": set.FromAddress,
		"sender_name":  set.SenderName,
	}
}

func (s *Server) getRelay(w http.ResponseWriter, r *http.Request) {
	set, err := s.store.SMTP(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, relayJSON(set, set.Configured()))
}

func (s *Server) putRelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host        string `json:"host"`
		Port        int    `json:"port"`
		TLS         string `json:"tls"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		FromAddress string `json:"from_address"`
		SenderName  string `json:"sender_name"`
	}
	if !decode(w, r, &req) {
		return
	}

	set := mail.Settings{
		Host:        req.Host,
		Port:        req.Port,
		TLS:         mail.TLS(req.TLS),
		Username:    req.Username,
		Password:    req.Password,
		FromAddress: req.FromAddress,
		SenderName:  req.SenderName,
	}
	// An empty password keeps the one already stored, which is what lets somebody correct a
	// port without retyping a credential the form was never given.
	if set.Password == "" {
		current, err := s.store.SMTP(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if current.Username == set.Username {
			set.Password = current.Password
		}
	}

	if err := s.store.SetSMTP(r.Context(), set); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay configured", "host", set.Host, "port", set.Port, "tls", set.TLS,
		"by", principal(r).Username)
	writeJSON(w, http.StatusOK, relayJSON(set, true))
}

func (s *Server) deleteRelay(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearSMTP(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("relay forgotten", "by", principal(r).Username)
	w.WriteHeader(http.StatusNoContent)
}

// testRelay sends one message and reports what the relay said.
//
// The whole reason the relay lives in the database rather than the environment: an operator
// gets it wrong two or three times, and each correction should be a form field and a press
// rather than a redeploy.
func (s *Server) testRelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To string `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.To) == "" {
		writeError(w, http.StatusBadRequest, "say where to send it")
		return
	}

	set, err := s.store.SMTP(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !set.Configured() {
		writeError(w, http.StatusBadRequest, "save a relay first")
		return
	}

	err = mail.Send(r.Context(), set, mail.Message{
		To:      req.To,
		Subject: "btw: the relay works",
		Body:    "This is a test from btw.\n\nIf you are reading it, the relay is configured correctly.\n",
	})
	if err != nil {
		// 502 rather than 500: everything on this side worked and something upstream did
		// not, and a 500 sends an operator through the wrong logs. The relay's own words go
		// with it — "the host was wrong", "the credentials were rejected" and "the
		// certificate did not verify" are three different afternoons.
		s.log.Warn("relay test failed", "host", set.Host, "err", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}
