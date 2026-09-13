package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"btw/internal/gateway"
)

// AboutLimit is how much somebody may write about themselves. Every word rides on every request
// the companion makes, so this is a token bill as much as a column width — a pasted CV should be
// refused at the form rather than found on an invoice.
const AboutLimit = 2000

// ModelLimit bounds a model slug: long enough for any router's names, short enough that the
// field cannot be used as storage.
const ModelLimit = 200

// Companion reads the model an account configured, or the zero value if there is none.
//
// A missing row is not an error. "No companion" is a state the interface renders — it is why
// nothing is being weighed — rather than a failure of the read.
func (s *Store) Companion(ctx context.Context, principalID string) (gateway.Settings, error) {
	var (
		set      gateway.Settings
		provider string
	)
	err := s.main.QueryRowContext(ctx,
		`SELECT provider, api_key, model, about FROM companion WHERE principal_id = ?`, principalID).
		Scan(&provider, &set.APIKey, &set.Model, &set.About)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return gateway.Settings{}, fmt.Errorf("read companion: %w", err)
	}
	set.Provider = gateway.Provider(provider).OrDefault()

	// Read here rather than left to the caller, since a caller that forgets asks a model
	// nobody chose. Keyed by provider: a slug belongs to one.
	set.Default, err = s.DefaultModel(ctx, set.Provider)
	if err != nil {
		return gateway.Settings{}, err
	}
	return set, nil
}

// SetCompanion replaces one account's companion.
func (s *Store) SetCompanion(ctx context.Context, principalID string, set gateway.Settings) error {
	set.APIKey = strings.TrimSpace(set.APIKey)
	set.Model = strings.TrimSpace(set.Model)
	set.About = strings.TrimSpace(set.About)

	if set.APIKey == "" {
		return Invalid("a companion needs a key")
	}
	if set.Provider != "" && !set.Provider.Valid() {
		return Invalid("%q is not a service this knows how to ask", set.Provider)
	}
	if len([]rune(set.Model)) > ModelLimit {
		return Invalid("that is more than %d characters for a model", ModelLimit)
	}
	if len([]rune(set.About)) > AboutLimit {
		// Runes, because the limit is on what somebody wrote and not on how it encodes. A
		// paragraph of Georgian is not four times as long as the same paragraph in English.
		return Invalid("that is more than %d characters about yourself", AboutLimit)
	}
	// The key's shape is not checked: a prefix rule would refuse a valid key the day a router
	// changes the format, and only using it settles the question. An empty model is stored empty
	// and means "whatever the default is now" — filling it in here would pin an account to
	// whichever default it first met.

	_, err := s.main.ExecContext(ctx,
		`INSERT INTO companion (principal_id, provider, api_key, model, about, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   provider = excluded.provider, api_key = excluded.api_key, model = excluded.model,
		   about = excluded.about, updated_at = excluded.updated_at`,
		principalID, string(set.Provider.OrDefault()), set.APIKey, set.Model, set.About, unix(s.Now()))
	if err != nil {
		return fmt.Errorf("set companion: %w", err)
	}
	return nil
}

// ClearCompanion forgets one account's companion, which is how somebody opts back out.
//
// A delete rather than a disabled flag. Somebody switching this off is withdrawing a
// credential and a description of their life, and leaving either behind against a row that
// says "off" is not what they asked for.
func (s *Store) ClearCompanion(ctx context.Context, principalID string) error {
	if _, err := s.main.ExecContext(ctx,
		`DELETE FROM companion WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("clear companion: %w", err)
	}
	return nil
}

// PrincipalsWithCompanion lists the accounts that have configured one.
//
// Ordered by id, which is chronological, so a pass works through people in the order they
// joined rather than in whatever order the page cache happened to hold.
func (s *Store) PrincipalsWithCompanion(ctx context.Context) ([]string, error) {
	rows, err := s.main.QueryContext(ctx,
		`SELECT principal_id FROM companion WHERE api_key != '' ORDER BY principal_id`)
	if err != nil {
		return nil, fmt.Errorf("list companions: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read companion row: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DefaultModel is the model an account on this provider follows when it has chosen none, or
// empty for the one compiled in.
//
// An administrator's, and instance-wide: routers retire slugs, and without this every account
// that never chose one breaks at the same moment with no way back but a redeploy. One per
// provider, because a slug belongs to one.
func (s *Store) DefaultModel(ctx context.Context, p gateway.Provider) (string, error) {
	var model string
	err := s.main.QueryRowContext(ctx,
		`SELECT model FROM companion_default WHERE provider = ?`, string(p.OrDefault())).Scan(&model)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the default model: %w", err)
	}
	return model, nil
}

// SetDefaultModel replaces it. Empty is how it is cleared, which puts the compiled-in one back.
func (s *Store) SetDefaultModel(ctx context.Context, p gateway.Provider, model string) error {
	if !p.Valid() {
		return Invalid("%q is not a service this knows how to ask", p)
	}
	model = strings.TrimSpace(model)
	if len([]rune(model)) > ModelLimit {
		return Invalid("that is more than %d characters for a model", ModelLimit)
	}
	if model == "" {
		if _, err := s.main.ExecContext(ctx,
			`DELETE FROM companion_default WHERE provider = ?`, string(p)); err != nil {
			return fmt.Errorf("clear the default model: %w", err)
		}
		return nil
	}
	_, err := s.main.ExecContext(ctx,
		`INSERT INTO companion_default (provider, model, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (provider) DO UPDATE SET model = excluded.model, updated_at = excluded.updated_at`,
		string(p), model, unix(s.Now()))
	if err != nil {
		return fmt.Errorf("set the default model: %w", err)
	}
	return nil
}
