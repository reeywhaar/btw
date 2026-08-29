package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"btw/internal/store"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	// Two buckets with two jobs. The global one bounds bcrypt: at cost 12 on an
	// unauthenticated endpoint it is a CPU exhaustion vector before it is an
	// authentication one. The per-username one is what stops somebody working through a
	// password list against one account, which the global limit alone would not — it would
	// only make them share the budget with everybody else.
	if !s.loginGlobal.allow("") || !s.loginPerUser.allow(strings.ToLower(req.Username)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait a minute")
		return
	}

	p, err := s.store.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		s.log.Info("login refused", "username", req.Username)
		s.fail(w, r, err)
		return
	}

	token := store.NewSessionToken()
	if err := s.store.CreateSession(r.Context(), token, p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSession(w, token)
	w.WriteHeader(http.StatusNoContent)
}

// changePassword replaces a password, ends every other session, and keeps this one.
//
// "Changing my password signs out my other devices" is what people mean by it, and signing
// them out of the tab they are typing in would be a strange way to confirm it worked.
//
// The store ends *every* session, this one included — which is the safe direction, since a
// half-applied change that left sessions alive behind a new password would be the bad kind
// of failure. So this mints a fresh session afterwards. The token is new rather than
// restored, which rotates the credential at exactly the moment somebody is worried enough
// about it to be here.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := principal(r)

	// Bounded even though the caller already holds a session: a borrowed one should not be
	// a place to guess the password at bcrypt's expense.
	if !s.passwordLimit.allow(p.ID) {
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait a minute")
		return
	}
	if _, err := s.store.Authenticate(r.Context(), p.Username, req.CurrentPassword); err != nil {
		s.log.Info("password change refused", "principal", p.ID)
		writeError(w, http.StatusBadRequest, "that is not your current password")
		return
	}

	if err := s.store.SetPassword(r.Context(), p.ID, req.NewPassword); err != nil {
		s.fail(w, r, err)
		return
	}

	token := store.NewSessionToken()
	if err := s.store.CreateSession(r.Context(), token, p.ID); err != nil {
		// The password did change. Saying so and sending them to sign in again is the
		// honest answer; pretending it failed would have them try the old one.
		s.log.Error("could not re-establish the session after a password change", "principal", p.ID, "err", err)
		s.clearSession(w)
		writeError(w, http.StatusOK, "your password changed, but you will need to sign in again")
		return
	}
	s.log.Info("password changed", "principal", p.ID)
	s.setSession(w, token)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		s.store.DeleteSession(r.Context(), cookie.Value)
	}
	s.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         p.ID,
		"username":   p.Username,
		"role":       p.Role,
		"created_at": p.CreatedAt.Unix(),
	})
}

// getInvite tells the acceptance page whether a link is live before somebody types a
// password into it. It reveals nothing but its own validity.
func (s *Server) getInvite(w http.ResponseWriter, r *http.Request) {
	inv, err := s.store.InviteByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"role":       inv.Role,
		"expires_at": inv.ExpiresAt.Unix(),
	})
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, err := s.store.AcceptInvite(r.Context(), r.PathValue("token"), req.Username, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Signed in immediately. Accepting an invitation and then being shown a login form is
	// asking somebody to prove something they just proved.
	token := store.NewSessionToken()
	if err := s.store.CreateSession(r.Context(), token, p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("account created", "principal", p.ID, "username", p.Username, "role", p.Role)
	s.setSession(w, token)
	w.WriteHeader(http.StatusNoContent)
}

// limiter is a fixed-window counter, per key.
//
// Fixed window rather than a token bucket: it lets through at most twice the rate across a
// window boundary, which for "ten login attempts a minute" is a distinction with no
// consequence, and it is ten lines instead of forty.
type limiter struct {
	mu     sync.Mutex
	seen   map[string]*window
	limit  int
	period time.Duration
}

type window struct {
	count int
	until time.Time
}

func newLimiter(limit int, period time.Duration) *limiter {
	return &limiter{seen: map[string]*window{}, limit: limit, period: period}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.seen[key]
	if !ok || now.After(w.until) {
		// Swept here rather than on a timer: the map is bounded by distinct usernames
		// tried within a minute, and anything that grows it is the attack this limits.
		if len(l.seen) > 10_000 {
			l.seen = map[string]*window{}
		}
		l.seen[key] = &window{count: 1, until: now.Add(l.period)}
		return true
	}
	w.count++
	return w.count <= l.limit
}
