package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"

	"btw/internal/mail"
)

// SMTP reads the relay an operator configured, or the zero value if there is none.
//
// A missing row is not an error. "No relay" is a state the interface renders — it is why a
// recovery address cannot be added yet — rather than a failure of the read.
func (s *Store) SMTP(ctx context.Context) (mail.Settings, error) {
	var set mail.Settings
	var tlsMode string
	err := s.main.QueryRowContext(ctx,
		`SELECT host, port, tls, username, password, from_address, sender_name
		   FROM smtp WHERE singleton = 1`).
		Scan(&set.Host, &set.Port, &tlsMode, &set.Username, &set.Password, &set.FromAddress, &set.SenderName)
	if errors.Is(err, sql.ErrNoRows) {
		return mail.Settings{}, nil
	}
	if err != nil {
		return mail.Settings{}, fmt.Errorf("read smtp: %w", err)
	}
	set.TLS = mail.TLS(tlsMode)
	return set, nil
}

// SetSMTP replaces the relay.
func (s *Store) SetSMTP(ctx context.Context, set mail.Settings) error {
	set.Host = strings.TrimSpace(set.Host)
	set.Username = strings.TrimSpace(set.Username)
	set.FromAddress = strings.TrimSpace(set.FromAddress)
	set.SenderName = strings.TrimSpace(set.SenderName)

	switch {
	case set.Host == "":
		return Invalid("a relay needs a host")
	case set.Port <= 0 || set.Port > 65535:
		return Invalid("%d is not a port", set.Port)
	case set.TLS != mail.StartTLS && set.TLS != mail.Implicit:
		// Whether the connection is encrypted is not configurable. A password crossing the
		// network in the clear is not a choice somebody should be able to make by accident.
		return Invalid("a relay is reached over STARTTLS or implicit TLS, and nothing else")
	case set.FromAddress == "":
		return Invalid("a relay needs an address to send from")
	}
	if _, err := netmail.ParseAddress(set.FromAddress); err != nil {
		return Invalid("%q is not an address the relay could send from", set.FromAddress)
	}
	// Accepting a blank password would make "this relay needs no authentication"
	// indistinguishable from "somebody left the field empty", and the second is far likelier.
	if set.Username != "" && set.Password == "" {
		return Invalid("a username needs a password")
	}
	if set.Password != "" && set.Username == "" {
		return Invalid("a password needs a username")
	}

	_, err := s.main.ExecContext(ctx,
		`INSERT INTO smtp (singleton, host, port, tls, username, password, from_address, sender_name, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (singleton) DO UPDATE SET
		   host = excluded.host, port = excluded.port, tls = excluded.tls,
		   username = excluded.username, password = excluded.password,
		   from_address = excluded.from_address, sender_name = excluded.sender_name,
		   updated_at = excluded.updated_at`,
		set.Host, set.Port, string(set.TLS), set.Username, set.Password,
		set.FromAddress, set.SenderName, unix(s.Now()))
	if err != nil {
		return fmt.Errorf("set smtp: %w", err)
	}
	return nil
}

// ClearSMTP forgets the relay, which is how an instance stops being able to send.
func (s *Store) ClearSMTP(ctx context.Context) error {
	if _, err := s.main.ExecContext(ctx, `DELETE FROM smtp WHERE singleton = 1`); err != nil {
		return fmt.Errorf("clear smtp: %w", err)
	}
	return nil
}

// SMTPUpdatedAt is when the relay was last saved, for an interface that wants to say so.
func (s *Store) SMTPUpdatedAt(ctx context.Context) (time.Time, error) {
	var at sql.NullInt64
	err := s.main.QueryRowContext(ctx, `SELECT updated_at FROM smtp WHERE singleton = 1`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read smtp: %w", err)
	}
	return timeFrom(at), nil
}
