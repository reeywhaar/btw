package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"btw/internal/openrouter"
)

// AboutLimit is how much somebody may write about themselves.
//
// Every word of it rides on every request the companion makes, so this is a token bill as
// much as a column width. Two thousand characters is several paragraphs — more than anybody
// has written about their own week — and the limit exists so that a pasted CV is refused at
// the form rather than discovered as a surprise on somebody's OpenRouter invoice.
const AboutLimit = 2000

// Companion reads the model an account configured, or the zero value if there is none.
//
// A missing row is not an error. "No companion" is a state the interface renders — it is why
// nothing is being weighed — rather than a failure of the read.
func (s *Store) Companion(ctx context.Context, principalID string) (openrouter.Settings, error) {
	var set openrouter.Settings
	err := s.main.QueryRowContext(ctx,
		`SELECT api_key, model, about FROM companion WHERE principal_id = ?`, principalID).
		Scan(&set.APIKey, &set.Model, &set.About)
	if errors.Is(err, sql.ErrNoRows) {
		return openrouter.Settings{}, nil
	}
	if err != nil {
		return openrouter.Settings{}, fmt.Errorf("read companion: %w", err)
	}
	return set, nil
}

// SetCompanion replaces one account's companion.
func (s *Store) SetCompanion(ctx context.Context, principalID string, set openrouter.Settings) error {
	set.APIKey = strings.TrimSpace(set.APIKey)
	set.Model = strings.TrimSpace(set.Model)
	set.About = strings.TrimSpace(set.About)

	if set.APIKey == "" {
		return Invalid("a companion needs a key")
	}
	if len([]rune(set.About)) > AboutLimit {
		// Runes, because the limit is on what somebody wrote and not on how it encodes. A
		// paragraph of Georgian is not four times as long as the same paragraph in English.
		return Invalid("that is more than %d characters about yourself", AboutLimit)
	}
	// The shape of the key is not checked, deliberately. A prefix rule would refuse a valid
	// key the day OpenRouter changes the format, and the only thing that actually settles
	// whether a key works is using it — which is what the test button is for.
	//
	// An empty model is stored empty. Filling in the default here was the tempting thing and
	// was wrong twice: it pinned an account to whatever the default was on the day it first
	// saved, and it handed the form back a model name where somebody had left a blank — so
	// the next save chose, on their behalf, something they never picked.

	_, err := s.main.ExecContext(ctx,
		`INSERT INTO companion (principal_id, api_key, model, about, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   api_key = excluded.api_key, model = excluded.model,
		   about = excluded.about, updated_at = excluded.updated_at`,
		principalID, set.APIKey, set.Model, set.About, unix(s.Now()))
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
