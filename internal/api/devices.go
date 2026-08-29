package api

import (
	"net/http"
	"strings"

	"btw/internal/ids"
	"btw/internal/store"
)

// pushKey is what the browser passes as applicationServerKey when it subscribes.
//
// Public, and it has to be: the page needs it before there is any question of a session,
// and it is a public key. What it reveals is that this instance sends push notifications,
// which it announces by existing.
func (s *Server) pushKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"key": s.push.PublicKey()})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Devices(r.Context(), principal(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		row := map[string]any{
			"id": d.ID,
			// The browser's own id, handed back so a browser can recognise its own row.
			// Not a secret from the person it belongs to — they minted it — and without it
			// a list of three "Chrome on Mac" says nothing about which is the one in front
			// of you and which are subscriptions that rotated out from under it.
			"client_id":     d.ClientID,
			"label":         d.Label,
			"created_at":    d.CreatedAt.Unix(),
			"last_ok_at":    nil,
			"failure_count": d.FailureCount,
			"last_error":    d.LastError,
		}
		if !d.LastOKAt.IsZero() {
			row["last_ok_at"] = d.LastOKAt.Unix()
		}
		// The endpoint never leaves the process. It is a capability: anybody holding it
		// and a VAPID key can put text on that lock screen.
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// createDevice registers a push subscription, and is idempotent on the endpoint — a
// browser re-registering an unchanged subscription updates the row it already has rather
// than growing the list every time somebody opens the app.
func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
		P256dh   string `json:"p256dh"`
		Auth     string `json:"auth"`
		Label    string `json:"label"`
		// Stable per browser, minted by the client and kept in localStorage. It is what
		// makes a rotated subscription replace its row instead of adding one.
		ClientID string `json:"client_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	d, err := s.store.RegisterDevice(r.Context(), principal(r).ID, req.Endpoint, req.P256dh, req.Auth, req.Label, req.ClientID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("device registered", "principal", principal(r).ID, "device", d.ID, "label", d.Label)
	writeJSON(w, http.StatusCreated, map[string]any{"id": d.ID, "label": d.Label})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.Valid(ids.Device, id) {
		writeError(w, http.StatusBadRequest, "that is not a device")
		return
	}
	if err := s.store.DeleteDevice(r.Context(), principal(r).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nudgeNow is the button that proves the chain — permission, subscription, VAPID,
// encryption, service worker, notification — in one press, without waiting hours for a
// slot.
//
// It stays in the product after it has served its purpose in development, because setting
// up a new phone raises exactly the same question.
func (s *Server) nudgeNow(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.nudgeNowLimit.allow(p.ID) {
		writeError(w, http.StatusTooManyRequests, "give it a minute")
		return
	}
	outcome, err := s.nudger.NudgeNow(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A 200 whatever the outcome: none of the three is a failure of the request. Which one
	// it was rides in the body, because "nothing to send" and "nothing reached your phone"
	// send somebody to two different places.
	writeJSON(w, http.StatusOK, map[string]any{
		"sent":    outcome == "sent",
		"outcome": outcome,
	})
}

func (s *Server) actOnNudge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.Valid(ids.Nudge, id) {
		writeError(w, http.StatusBadRequest, "that is not a nudge")
		return
	}
	// The last path segment is the verb, so one handler serves both buttons and they
	// cannot drift apart.
	action := store.ActionDone
	if strings.HasSuffix(r.URL.Path, "/"+store.ActionDrop) {
		action = store.ActionDrop
	}

	p := principal(r)
	reminderID, err := s.store.ActOnNudge(r.Context(), p.ID, id, action)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.store.EndReminder(r.Context(), p.ID, reminderID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("nudge answered", "principal", p.ID, "nudge", id, "action", action)
	w.WriteHeader(http.StatusNoContent)
}
