package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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

// unattended is what btw writes into main.db on its own as it delivers a nudge. A copy is worth
// taking when somebody changed something, and these ride along with the next one that is.
var unattended = map[string][]string{
	"reminders": {"last_nudged_at"},
	"devices":   {"last_ok_at", "failure_count", "last_error"},
}

// MainDigest identifies what main.db holds, leaving out [unattended].
func (s *Store) MainDigest(ctx context.Context) ([]byte, error) {
	tx, err := s.main.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	h := sha256.New()
	tables, err := digestSchema(ctx, tx, h)
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		if err := digestTable(ctx, tx, h, table); err != nil {
			return nil, fmt.Errorf("digest %s: %w", table, err)
		}
	}
	return h.Sum(nil), nil
}

func digestSchema(ctx context.Context, tx *sql.Tx, h io.Writer) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT type, name, coalesce(sql, '') FROM sqlite_schema ORDER BY type, name`)
	if err != nil {
		return nil, fmt.Errorf("read the schema: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var kind, name, def string
		if err := rows.Scan(&kind, &name, &def); err != nil {
			return nil, err
		}
		digestValues(h, kind, name, def)
		if kind == "table" && !strings.HasPrefix(name, "sqlite_") {
			tables = append(tables, name)
		}
	}
	return tables, rows.Err()
}

func digestTable(ctx context.Context, tx *sql.Tx, h io.Writer, table string) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return err
	}
	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			rows.Close()
			return err
		}
		if !slices.Contains(unattended[table], col) {
			cols = append(cols, quoteIdent(col))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}

	order := make([]string, len(cols))
	for i := range cols {
		order[i] = strconv.Itoa(i + 1)
	}
	rows, err = tx.QueryContext(ctx, "SELECT "+strings.Join(cols, ", ")+" FROM "+quoteIdent(table)+
		" ORDER BY "+strings.Join(order, ", "))
	if err != nil {
		return err
	}
	defer rows.Close()

	digestValues(h, "table", table, strings.Join(cols, ","))
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		io.WriteString(h, "row ")
		digestValues(h, values...)
	}
	return rows.Err()
}

func digestValues(h io.Writer, values ...any) {
	for _, v := range values {
		var b []byte
		switch v := v.(type) {
		case nil:
		case []byte:
			b = v
		case string:
			b = []byte(v)
		default:
			b = fmt.Append(nil, v)
		}
		fmt.Fprintf(h, "%T %d:", v, len(b))
		h.Write(b)
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
