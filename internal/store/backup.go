package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SnapshotFile is one database, whole, in memory.
type SnapshotFile struct {
	// Name is the file's name in the data directory — main.db, derived.db — so an archive
	// built from these extracts straight back over the mount.
	Name string
	Data []byte
}

// Snapshot copies the databases into memory, consistently, while they are being written.
//
// `VACUUM INTO`, which is the whole reason this exists as a method rather than as a `tar` over
// the mounted directory. A SQLite database in WAL mode is not one file: it is main.db,
// main.db-wal and main.db-shm, with the committed state spread across them. Copy those three
// at slightly different moments — which is what any file-level backup of a running service
// does — and what you have restored is a database as it never was. `VACUUM INTO` asks SQLite
// for the database as of one read transaction, with the log folded in, and hands back a single
// self-contained file with no sidecars.
//
// derived is off unless asked for, which is the two-database split showing up in the backup
// policy: main.db is what somebody typed and cannot be recovered from anywhere, derived.db is
// sessions, the nudge waiting to go out and the nudge log. How much archive to carry is the
// operator's judgement, which is why it is a setting and not a rule.
func (s *Store) Snapshot(ctx context.Context, derived bool) ([]SnapshotFile, error) {
	// A directory of this process's own rather than the data directory: VACUUM INTO refuses
	// to overwrite, so it needs somewhere nothing else is writing, and a failed run should not
	// leave a half-written database sitting beside the real ones where the next thing along
	// might mistake it for one.
	tmp, err := os.MkdirTemp("", "btw-snapshot-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	// main.db first. It is the file that must survive, so it is the first thing out of the
	// archive and the first thing back into a data directory.
	sources := []struct {
		name string
		db   *sql.DB
	}{{MainFile, s.main}}
	if derived {
		sources = append(sources, struct {
			name string
			db   *sql.DB
		}{DerivedFile, s.derived})
	}

	out := make([]SnapshotFile, 0, len(sources))
	for _, source := range sources {
		path := filepath.Join(tmp, source.name)
		if _, err := source.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", source.name, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read the %s snapshot: %w", source.name, err)
		}
		out = append(out, SnapshotFile{Name: source.name, Data: data})
	}
	return out, nil
}

// LastBackup is the digest of main.db as it was when a copy was last accepted by a backup
// agent, and when that was. A nil digest means none ever has been.
//
// In derived.db — see the migration — because the answer is about main.db, and writing it into
// main.db would change the thing being asked about.
func (s *Store) LastBackup(ctx context.Context) ([]byte, time.Time, error) {
	var (
		digest []byte
		at     int64
	)
	err := s.derived.QueryRowContext(ctx,
		`SELECT digest, pushed_at FROM backup_state WHERE singleton = 1`).Scan(&digest, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return digest, time.Unix(at, 0).UTC(), nil
}

// RecordBackup remembers what was sent, so an unchanged database is not sent again.
//
// Written only after the agent has accepted the archive. Recorded before, a rejected upload
// would leave this process believing a copy exists that does not — and the next change to
// main.db would be the only thing that ever made it try again.
func (s *Store) RecordBackup(ctx context.Context, digest []byte, at time.Time) error {
	_, err := s.derived.ExecContext(ctx, `
		INSERT INTO backup_state (singleton, digest, pushed_at) VALUES (1, ?, ?)
		ON CONFLICT (singleton) DO UPDATE SET
			digest = excluded.digest, pushed_at = excluded.pushed_at`,
		digest, at.UTC().Unix())
	return err
}
