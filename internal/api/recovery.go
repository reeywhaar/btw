package api

import (
	"net/http"

	"btw/internal/mail"
)

// getRecovery says what address the account can be recovered through, what it is partway
// through proving, and whether this instance can send mail at all.
//
// `mail_configured` is not a secret from the people whose recovery depends on it: a button
// that is disabled without saying why sends somebody looking for the reason in the wrong
// place.
func (s *Server) getRecovery(w http.ResponseWriter, r *http.Request) {
	p := principal(r)

	email, provedAt, err := s.store.RecoveryAddress(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pending, err := s.store.PendingRecovery(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	set, err := s.store.SMTP(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := map[string]any{
		"email":           email,
		"proved_at":       nil,
		"pending":         pending,
		"mail_configured": set.Configured(),
	}
	if !provedAt.IsZero() {
		out["proved_at"] = provedAt.Unix()
	}
	writeJSON(w, http.StatusOK, out)
}

// startRecovery sends a code to an address, and records nothing that recovery could read.
//
// Two steps, because storing whatever was typed is worse than storing nothing: a typo points
// recovery at a stranger's inbox and the owner finds out at the one moment they cannot
// afford to. It is also the shape of an attack — a borrowed session sets an address of its
// own and comes back for the account later.
func (s *Server) startRecovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := principal(r)

	set, err := s.store.SMTP(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Refused before anything is written. An address stored against a relay that does not
	// exist is a promise the product cannot keep.
	if !set.Configured() {
		writeError(w, http.StatusBadRequest,
			"this instance cannot send mail yet; an administrator has to configure a relay")
		return
	}

	code, err := s.store.StartRecovery(r.Context(), p.ID, req.Email)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	err = mail.Send(r.Context(), set, mail.Message{
		To:      req.Email,
		Subject: "btw: confirm this address",
		Body: "Somebody asked to use this address to recover the btw account " + p.Username + ".\n\n" +
			"The code is " + code + "\n\n" +
			"It is good for fifteen minutes. If this was not you, ignore it — nothing has\n" +
			"changed, and the address is not on the account unless the code goes back.\n",
	})
	if err != nil {
		// A code that could not be sent leaves nothing waiting. Otherwise the page says it
		// is waiting on a code that never left, and the way out of that state is the button
		// somebody just watched fail.
		s.store.DropRecovery(r.Context(), p.ID)
		s.log.Warn("recovery code could not be sent", "principal", p.ID, "err", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// The address is logged nowhere. It is not proved yet, and until it is, it is a string
	// somebody typed.
	s.log.Info("recovery code sent", "principal", p.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) confirmRecovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := principal(r)

	email, err := s.store.ConfirmRecovery(r.Context(), p.ID, req.Code)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("recovery address proved", "principal", p.ID)
	writeJSON(w, http.StatusOK, map[string]any{"email": email})
}

func (s *Server) forgetRecovery(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ForgetRecovery(r.Context(), principal(r).ID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
