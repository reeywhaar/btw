package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"btw/internal/ids"
)

// Device is a browser that has agreed to receive nudges. What a person sees in settings:
// "This phone", "Chrome on the laptop".
type Device struct {
	ID           string
	PrincipalID  string
	Endpoint     string
	P256dh       string
	Auth         string
	Label        string
	ClientID     string
	CreatedAt    time.Time
	LastOKAt     time.Time
	FailureCount int
	LastError    string
}

// MaxDeviceLabel keeps a user-agent-derived label to something a line of interface can
// hold.
const MaxDeviceLabel = 100

// RegisterDevice records a push subscription, and is idempotent on the endpoint: a browser
// re-registering an unchanged subscription updates the row it already has rather than
// growing the list by one every time somebody opens the app.
//
// The endpoint is globally unique, so registering one takes it from whoever held it
// before. That is a privacy property rather than tidiness: one browser profile has one
// subscription, and if somebody signs out and somebody else signs in, the same endpoint is
// offered again. Scoped per principal, both rows would live and the first person's
// reminders would arrive on a device the second person is holding.
func (s *Store) RegisterDevice(ctx context.Context, principalID, endpoint, p256dh, auth, label, clientID string) (Device, error) {
	switch {
	case strings.TrimSpace(endpoint) == "":
		return Device{}, Invalid("a subscription needs an endpoint")
	case !strings.HasPrefix(endpoint, "https://"):
		// Push services are https without exception, and an endpoint that is not is either
		// a mistake or an attempt to make this process fetch something else.
		return Device{}, Invalid("a push endpoint must be https")
	case p256dh == "" || auth == "":
		return Device{}, Invalid("a subscription needs both of its keys")
	}
	if len([]rune(label)) > MaxDeviceLabel {
		label = string([]rune(label)[:MaxDeviceLabel])
	}
	if len([]rune(clientID)) > MaxDeviceLabel {
		return Device{}, Invalid("that client id is too long")
	}

	// One browser, one row.
	//
	// An endpoint identifies a subscription rather than a browser, and browsers replace
	// subscriptions on their own. Upserting on the endpoint alone left the old row in place,
	// both were live at the push service, and one press sent two pushes — which is one
	// browser showing two notifications, and is what this line exists to stop.
	//
	// Empty is never matched: a row from before this column existed has no browser to
	// belong to, and must not collapse somebody else's.
	if clientID != "" {
		if _, err := s.main.ExecContext(ctx,
			`DELETE FROM devices WHERE principal_id = ? AND client_id = ? AND endpoint <> ?`,
			principalID, clientID, endpoint); err != nil {
			return Device{}, fmt.Errorf("replace device: %w", err)
		}
	}

	now := s.Now().Truncate(time.Second)
	d := Device{
		ID:          ids.New(ids.Device),
		PrincipalID: principalID,
		Endpoint:    endpoint,
		P256dh:      p256dh,
		Auth:        auth,
		Label:       label,
		ClientID:    clientID,
		CreatedAt:   now,
	}
	// ON CONFLICT rather than a read-then-write, so two tabs registering at once produce
	// one row. principal_id is in the update list because that is the sign-out case: the
	// endpoint moves to whoever registered it last, and its failure history is reset
	// because it is now somebody else's device.
	err := s.main.QueryRowContext(ctx,
		`INSERT INTO devices (id, principal_id, endpoint, p256dh, auth, label, client_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (endpoint) DO UPDATE SET
		   principal_id  = excluded.principal_id,
		   p256dh        = excluded.p256dh,
		   auth          = excluded.auth,
		   label         = excluded.label,
		   client_id     = excluded.client_id,
		   failure_count = 0,
		   last_error    = ''
		 RETURNING id, created_at`,
		d.ID, d.PrincipalID, d.Endpoint, d.P256dh, d.Auth, d.Label, d.ClientID, unix(d.CreatedAt)).
		Scan(&d.ID, new(int64))
	if err != nil {
		return Device{}, fmt.Errorf("register device: %w", err)
	}
	return d, nil
}

// Devices lists one person's.
func (s *Store) Devices(ctx context.Context, principalID string) ([]Device, error) {
	rows, err := s.main.QueryContext(ctx,
		`SELECT id, principal_id, endpoint, p256dh, auth, label, client_id, created_at, last_ok_at, failure_count, last_error
		   FROM devices WHERE principal_id = ? ORDER BY id`, principalID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		var (
			d      Device
			cre    int64
			lastOK sql.NullInt64
		)
		if err := rows.Scan(&d.ID, &d.PrincipalID, &d.Endpoint, &d.P256dh, &d.Auth, &d.Label,
			&d.ClientID, &cre, &lastOK, &d.FailureCount, &d.LastError); err != nil {
			return nil, fmt.Errorf("read device: %w", err)
		}
		d.CreatedAt = time.Unix(cre, 0).UTC()
		d.LastOKAt = timeFrom(lastOK)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice removes one, scoped to its owner.
func (s *Store) DeleteDevice(ctx context.Context, principalID, id string) error {
	res, err := s.main.ExecContext(ctx, `DELETE FROM devices WHERE id = ? AND principal_id = ?`, id, principalID)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("no device %s", id)
	}
	return nil
}

// DeleteDeviceByID removes one without knowing whose it is, which is what a 410 from a
// push service means: that endpoint is gone and is not coming back.
func (s *Store) DeleteDeviceByID(ctx context.Context, id string) error {
	if _, err := s.main.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	return nil
}

// DeviceDelivered records a successful push and clears the failure history.
func (s *Store) DeviceDelivered(ctx context.Context, id string) error {
	if _, err := s.main.ExecContext(ctx,
		`UPDATE devices SET last_ok_at = ?, failure_count = 0, last_error = '' WHERE id = ?`,
		unix(s.Now()), id); err != nil {
		return fmt.Errorf("stamp device: %w", err)
	}
	return nil
}

// DeviceFailed records why a push did not land. The reason is kept because "this device
// stopped receiving" without "and here is what the push service said" sends somebody to
// logs they do not have.
func (s *Store) DeviceFailed(ctx context.Context, id, reason string) error {
	if _, err := s.main.ExecContext(ctx,
		`UPDATE devices SET failure_count = failure_count + 1, last_error = ? WHERE id = ?`,
		reason, id); err != nil {
		return fmt.Errorf("stamp device failure: %w", err)
	}
	return nil
}

// PrincipalsWithDevices is who the scheduler plans a day for.
//
// Planning for somebody with nowhere to deliver would fill a table with rows that can only
// ever be dropped, and a disabled account should not be woken up by anything.
func (s *Store) PrincipalsWithDevices(ctx context.Context) ([]string, error) {
	rows, err := s.main.QueryContext(ctx,
		`SELECT DISTINCT p.id FROM principals p
		   JOIN devices d ON d.principal_id = p.id
		  WHERE p.disabled_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("list principals with devices: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read principal id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
