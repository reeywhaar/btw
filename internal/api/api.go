// Package api is btw's HTTP surface: handlers, the two authorisation decisions, and the
// SPA.
//
// Routing is net/http with method patterns, and the authorisation is made at registration
// rather than inside a handler. A handler cannot forget to check, because a handler
// registered without a guard is visibly registered without one.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"btw/internal/app"
	"btw/internal/config"
	"btw/internal/store"
	"btw/internal/webpush"
)

// SessionCookie is the only credential this service issues. There is no bearer token, no
// API key, and no header-based auth of any kind.
const SessionCookie = "btw_auth"

// maxBody caps a request body. Every JSON document this accepts is a few hundred bytes.
const maxBody = 64 << 10

// Scheduler is what the API needs from internal/nudge: the ability to send one now.
//
// One method, because a rhythm change needs no announcing any more. There is no plan to
// redraw — whether somebody is owed a nudge is worked out from the clock on the next tick,
// so a change applies within five minutes of being saved by doing nothing at all.
//
// An interface rather than the concrete type, so internal/api does not import the package
// that imports it — and so a test can drive the button without a push service on the other
// end.
type Scheduler interface {
	NudgeNow(ctx context.Context, principalID string) (outcome string, delivered int, err error)
}

// Server holds what every handler needs.
type Server struct {
	cfg    *config.Config
	store  *store.Store
	log    *slog.Logger
	push   *webpush.Sender
	nudger Scheduler
	spa    *SPA

	// Per-server rather than package-level. Two instances in one process — which is what
	// a test suite is — would otherwise share one budget and lock each other out.
	loginGlobal   *limiter
	loginPerUser  *limiter
	passwordLimit *limiter
	nudgeNowLimit *limiter
}

// New builds the handler tree.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, push *webpush.Sender, nudger Scheduler, spa *SPA) *Server {
	return &Server{
		cfg: cfg, store: st, log: log, push: push, nudger: nudger, spa: spa,
		loginGlobal:   newLimiter(60, time.Minute),
		loginPerUser:  newLimiter(5, time.Minute),
		passwordLimit: newLimiter(5, time.Minute),
		// The test button makes an outbound request on the caller's behalf, and an
		// endpoint that does that needs a ceiling.
		nudgeNowLimit: newLimiter(6, time.Minute),
	}
}

// Handler is the whole routing table, which is short enough to read in one screen — and
// that is the point. Every line either carries a guard or is deliberately public.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)

	// Everything about proving who you are lives under /api/auth. Signing in, signing out
	// and asking who you are were three unrelated-looking paths at the top level, next to
	// the resources they are not — and an invitation is the same subject, since accepting
	// one is how an account starts.
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("GET /api/auth/invites/{token}", s.getInvite)
	mux.HandleFunc("POST /api/auth/invites/{token}/accept", s.acceptInvite)
	mux.Handle("POST /api/auth/logout", s.requireSession(s.logout))
	mux.Handle("GET /api/auth/me", s.requireSession(s.me))
	mux.Handle("POST /api/auth/password", s.requireSession(s.changePassword))

	// An address the account can be recovered through. Under /api/auth because that is what
	// it recovers, and because it is the same subject as everything else here.
	mux.Handle("GET /api/auth/recovery", s.requireSession(s.getRecovery))
	mux.Handle("POST /api/auth/recovery", s.requireSession(s.startRecovery))
	mux.Handle("POST /api/auth/recovery/confirm", s.requireSession(s.confirmRecovery))
	mux.Handle("DELETE /api/auth/recovery", s.requireSession(s.forgetRecovery))

	mux.HandleFunc("GET /api/push/key", s.pushKey)

	mux.Handle("GET /api/reminders", s.requireSession(s.listReminders))
	mux.Handle("POST /api/reminders", s.requireSession(s.createReminder))
	mux.Handle("PATCH /api/reminders/{id}", s.requireSession(s.updateReminder))
	mux.Handle("POST /api/reminders/{id}/done", s.requireSession(s.endReminder))
	mux.Handle("POST /api/reminders/{id}/revive", s.requireSession(s.reviveReminder))
	mux.Handle("DELETE /api/reminders/{id}", s.requireSession(s.deleteReminder))

	mux.Handle("GET /api/rhythm", s.requireSession(s.getRhythm))
	mux.Handle("PATCH /api/rhythm", s.requireSession(s.patchRhythm))

	mux.Handle("GET /api/devices", s.requireSession(s.listDevices))
	mux.Handle("POST /api/devices", s.requireSession(s.createDevice))
	mux.Handle("DELETE /api/devices/{id}", s.requireSession(s.deleteDevice))
	// POST /api/nudges creates one now — which is what the button does, and reads as what
	// it does. It was /api/nudge, a singular root beside a plural one for the same subject.
	mux.Handle("POST /api/nudges", s.requireSession(s.nudgeNow))

	// The two the service worker calls. Same-origin from a worker, so the session cookie
	// rides along under SameSite=Lax and Sec-Fetch-Site says same-origin.
	mux.Handle("POST /api/nudges/{id}/done", s.requireSession(s.actOnNudge))
	mux.Handle("POST /api/nudges/{id}/drop", s.requireSession(s.actOnNudge))

	// Administrators only. The mail relay is instance-wide configuration, which is why it
	// lives here rather than on anybody's own settings.
	mux.Handle("GET /api/admin/relay", s.requireAdmin(s.getRelay))
	mux.Handle("PUT /api/admin/relay", s.requireAdmin(s.putRelay))
	mux.Handle("DELETE /api/admin/relay", s.requireAdmin(s.deleteRelay))
	mux.Handle("POST /api/admin/relay/test", s.requireAdmin(s.testRelay))

	// A catch-all under /api, so a mistyped path comes back as JSON rather than falling
	// through to the SPA and reaching a fetch as an HTML document it cannot parse.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})

	mux.Handle("/", s.spa)

	return s.guard(mux)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": app.Version})
}

// guard is the CSRF defence, applied to everything.
//
// Three parts, all cheap, and the absence of a CORS middleware is the fourth: the browser
// only ever talks to this origin, and adding Access-Control-Allow-Origin would weaken two
// of these three. A test asserts the header is never emitted.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// A browser sets this and a script cannot forge it. A service worker on this
		// origin sends same-origin, which is what makes the notification buttons work
		// without an exception here.
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "that request came from somewhere else")
			return
		}
		// Checked only when a body is actually present: a DELETE legitimately carries
		// none, and demanding a content type for an absent body is a rule that only ever
		// catches our own client.
		if r.ContentLength != 0 {
			ct := r.Header.Get("Content-Type")
			if media, _, _ := strings.Cut(ct, ";"); strings.TrimSpace(media) != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "send JSON")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type principalKey struct{}

// principal is the account this request belongs to. Present on every handler behind
// requireSession, and on no other.
func principal(r *http.Request) store.Principal {
	p, _ := r.Context().Value(principalKey{}).(store.Principal)
	return p
}

// requireSession is one of only two authorisation decisions in the program, and it is made
// where the route is registered.
func (s *Server) requireSession(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		sess, moved, err := s.store.Session(r.Context(), cookie.Value)
		if err != nil {
			// Never a redirect. A 302 to an HTML page is the least useful thing a fetch
			// can receive; the island reads the 401 and sends somebody to /login itself.
			s.clearSession(w)
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		p, err := s.store.Principal(r.Context(), sess.PrincipalID)
		if err != nil || p.Disabled() {
			s.store.DeleteSession(r.Context(), cookie.Value)
			s.clearSession(w)
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		if moved {
			s.setSession(w, cookie.Value)
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

func (s *Server) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(store.SessionLifetime.Seconds()),
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// decode reads a JSON body into v, with a size limit and no unknown fields.
//
// Into a request struct, never into a map: a map accepts anything and moves every
// validation into the handler, one forgotten check at a time.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "that request had no body")
			return false
		}
		writeError(w, http.StatusBadRequest, "that request body could not be read")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps a store error onto a status code. The one place in the program that does, so
// two handlers cannot disagree about what a conflict is.
//
// The message is the store's own, because it was written for the person who will read it
// in the interface rather than for a log grep.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		// Logged with the path, because an unclassified error is a bug and the message
		// that reaches the browser deliberately does not say what it was.
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "something went wrong here")
	}
}
