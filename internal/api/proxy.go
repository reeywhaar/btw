package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"btw/internal/gateway"
	"btw/internal/proxy"
)

// A test press reaches the gateways themselves, on the one route each that needs no key.
//
// Testing against anything else — an address somebody types, a service that echoes an IP —
// would prove a proxy works and leave the only question that matters unanswered: a proxy that
// reaches everything except the gateway is one somebody would otherwise have called working.
//
// Every service, because they are separate hosts and are blocked separately. An account on the
// one that cannot be reached is not helped by the other one answering.

// probeTimeout bounds one test press. Short, because somebody is watching it.
const probeTimeout = 30 * time.Second

// proxyJSON is the proxy as it goes out, which is everything except the credential.
//
// A stored token is never sent back, for the reason docs/mail.md gives about the relay's
// password. `token_set` is what the form needs, and an empty token on save keeps the stored
// one — which is what lets somebody correct an address without retyping a secret they cannot
// read off the screen.
func proxyJSON(set proxy.Settings) map[string]any {
	return map[string]any{
		"configured": set.Configured(),
		"kind":       string(set.Kind),
		"url":        set.URL,
		"username":   set.Username,
		"token_set":  set.Token != "",
		"enabled":    set.Enabled,
	}
}

func (s *Server) getProxy(w http.ResponseWriter, r *http.Request) {
	set, err := s.store.Proxy(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, proxyJSON(set))
}

func (s *Server) putProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind     string `json:"kind"`
		URL      string `json:"url"`
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}

	set := proxy.Settings{
		Kind:     proxy.Kind(req.Kind),
		URL:      req.URL,
		Username: req.Username,
		Token:    req.Token,
	}
	// An empty token keeps the one already stored, and only when the rest of it still names
	// the same endpoint. Carrying a proxio token over to a socks host would send a secret
	// somewhere it was never meant for.
	if set.Token == "" {
		current, err := s.store.Proxy(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if current.Kind == set.Kind && current.URL == set.URL {
			set.Token = current.Token
		}
	}

	if err := s.store.SetProxy(r.Context(), set); err != nil {
		s.fail(w, r, err)
		return
	}
	// The kind and the host, never the token and never the username. A log line is the other
	// place a credential ends up written down.
	s.log.Info("proxy configured", "kind", set.Kind, "by", principal(r).Username)
	s.getProxy(w, r)
}

// patchProxy switches an existing proxy on or off.
//
// Its own route rather than a field on the save, because they are different acts: saving says
// "this is what it should be", and this says "leave it exactly as it is and stop using it".
// Folding the second into the first would mean the only way to switch one off was to send its
// settings back, which is the request most likely to be sent by something that read them
// stale.
func (s *Server) patchProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "say whether it should be on or off")
		return
	}

	if err := s.store.EnableProxy(r.Context(), *req.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("proxy switched", "enabled", *req.Enabled, "by", principal(r).Username)
	s.getProxy(w, r)
}

func (s *Server) deleteProxy(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearProxy(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("proxy forgotten", "by", principal(r).Username)
	w.WriteHeader(http.StatusNoContent)
}

// testProxy reaches the gateway through the saved proxy and reports what came back.
//
// Against the saved settings, and deliberately **ignoring whether it is switched on**: the
// press means "would this work", and refusing to answer that for a proxy somebody has just
// switched off in order to test it would be answering a different question.
func (s *Server) testProxy(w http.ResponseWriter, r *http.Request) {
	set, err := s.store.Proxy(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !set.Configured() {
		writeError(w, http.StatusBadRequest, "save a proxy first")
		return
	}
	set.Enabled = true

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	started := time.Now()
	reached := make([]map[string]any, 0, len(gateway.Providers()))
	for _, p := range gateway.Providers() {
		if err := reach(ctx, p.ProbeTarget(), set); err != nil {
			// 502 rather than 500, for the reason docs/mail.md gives about a refused send:
			// everything on this side worked and something upstream did not. The message has
			// already had any address scrubbed out of it — see proxy.Send.
			s.log.Warn("proxy test failed", "kind", set.Kind, "service", p, "err", err)
			writeError(w, http.StatusBadGateway, p.Label()+": "+err.Error())
			return
		}
		reached = append(reached, map[string]any{"label": p.Label(), "url": p.ProbeTarget()})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"reached": reached,
		"took_ms": time.Since(started).Milliseconds(),
	})
}

func reach(ctx context.Context, target string, set proxy.Settings) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := proxy.Send(ctx, req, set)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Read and discarded, bounded: the answer is a long list of models and nothing here wants
	// it, but a body left unread is a connection that cannot be reused.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		// A proxy that answers but hands back a refusal is not working, whatever the transport
		// did. Saying so with the status is what tells an operator whether to look at the
		// proxy or at the gateway.
		return errors.New("the proxy was reached and the gateway answered " + resp.Status)
	}
	return nil
}
