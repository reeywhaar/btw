// Package store owns both SQLite databases and every SQL statement in the program.
//
// # Two databases, split on what is worth backing up
//
// main.db holds what a person typed: accounts, invitations, reminders, tags, rhythm,
// devices, and the VAPID keypair. derived.db holds what the running process accumulated:
// sessions, the day's slots, the nudge log.
//
// The name is a leftover and is not quite right: almost nothing in derived.db is derived
// from anything. The admission test is simpler and stricter — can this file be deleted
// between two runs without anybody losing something they wrote? Sessions are not
// recomputable from the accounts they belong to; they are throwaway all the same, because
// the whole cost of losing them is that everybody signs in again.
//
// Holding that seam is what keeps main.db small and quiet. It is written when somebody
// types something and at almost no other time, which is what a file you take snapshots of
// should look like.
//
// They are separate handles and are never ATTACHed. SQLite's atomic multi-database commit
// does not hold under journal_mode=WAL, so a transaction spanning both would be a
// transaction only on paper. No foreign key crosses the boundary either.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go, no cgo, so the image stays static

	"btw/internal/store/migrations"
)

// The error vocabulary handlers map onto status codes. Three sentinels, wrapped with
// context where they are returned, translated in exactly one place in internal/api — a
// handler that switched on a driver error would eventually disagree with another handler.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)

// File names within the data directory.
const (
	MainFile    = "main.db"
	DerivedFile = "derived.db"
)

// Store is both databases.
type Store struct {
	main    *sql.DB
	derived *sql.DB

	// now is injectable so tests drive expiry without sleeping.
	now func() time.Time
}

// Open opens both databases, creating the directory and the files if they are absent, and
// migrates each forward.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	main, err := open(filepath.Join(dir, MainFile))
	if err != nil {
		return nil, err
	}
	derived, err := open(filepath.Join(dir, DerivedFile))
	if err != nil {
		main.Close()
		return nil, err
	}

	s := &Store{main: main, derived: derived, now: time.Now}

	ctx := context.Background()
	if err := migrate(ctx, main, derived, MainFile, migrations.Main); err != nil {
		s.Close()
		return nil, err
	}
	if err := migrate(ctx, derived, main, DerivedFile, migrations.Derived); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// SchemaVersions reports what each database has applied. Logged at startup, because "did
// my deployment actually migrate?" is otherwise answered by shelling into a container —
// and the moment somebody wants the answer is the moment something has gone wrong.
func (s *Store) SchemaVersions(ctx context.Context) (main, derived int, err error) {
	if err = s.main.QueryRowContext(ctx, "PRAGMA user_version").Scan(&main); err != nil {
		return 0, 0, fmt.Errorf("read %s version: %w", MainFile, err)
	}
	if err = s.derived.QueryRowContext(ctx, "PRAGMA user_version").Scan(&derived); err != nil {
		return 0, 0, fmt.Errorf("read %s version: %w", DerivedFile, err)
	}
	return main, derived, nil
}

// SetClock replaces the clock. For tests; the daemon never calls it.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// Now is the store's clock, in UTC.
func (s *Store) Now() time.Time { return s.now().UTC() }

// Close releases both databases.
func (s *Store) Close() error { return errors.Join(s.main.Close(), s.derived.Close()) }

func open(path string) (*sql.DB, error) {
	// WAL so a write never blocks a read; busy_timeout so a concurrent statement waits
	// rather than failing with SQLITE_BUSY; foreign_keys because SQLite leaves them off by
	// default and a schema full of REFERENCES that enforces nothing is worse than no
	// schema at all.
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"busy_timeout(5000)",
			"synchronous(NORMAL)",
			"foreign_keys(ON)",
		},
	}.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One connection. Writes are serialised anyway, and this removes lock contention as a
	// category of bug — including the one where a pooled connection is handed out before
	// its pragmas have been applied.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := verifyPragmas(db, path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// verifyPragmas checks that the DSN's pragmas actually took.
//
// A pragma in a DSN is a request, not a guarantee: a driver that ignored one leaves a
// database that looks fine and behaves differently. Both of these are load-bearing —
// foreign_keys=OFF permits orphaned rows for the lifetime of the connection — and finding
// out at startup is much cheaper than finding out from the data.
func verifyPragmas(db *sql.DB, path string) error {
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("%s: read journal_mode: %w", path, err)
	}
	if journal != "wal" {
		return fmt.Errorf("%s: journal_mode is %q, expected wal", path, journal)
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("%s: read foreign_keys: %w", path, err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("%s: foreign_keys is off", path)
	}
	return nil
}

// migrate applies every migration the database has not seen, tracked by PRAGMA
// user_version. Each runs in its own transaction that also stamps the version, so a
// failure leaves the database at the last version that fully applied rather than half way
// through one.
func migrate(ctx context.Context, db, other *sql.DB, name string, list []migrations.Migration) error {
	// Sorted by name, which begins with a timestamp, so a file added out of order still
	// applies in the right place. The declared order in migrations.go is documentation;
	// this is what decides.
	list = slices.SortedFunc(slices.Values(list), func(a, b migrations.Migration) int {
		return strings.Compare(a.Name, b.Name)
	})

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("%s: read user_version: %w", name, err)
	}
	// Refusing rather than proceeding: a binary that does not understand the schema in
	// front of it cannot know which of its statements are still correct, and downgrading
	// is not supported. Saying so beats corrupting data quietly.
	if version > len(list) {
		return fmt.Errorf("%s: schema version %d is newer than this build understands (%d); downgrading is not supported",
			name, version, len(list))
	}

	for i := version; i < len(list); i++ {
		step := list[i]
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", name, step.Name, err)
		}
		if err := step.Up(migrations.Context{Ctx: ctx, Tx: tx, Other: other}); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %s: %w", name, step.Name, err)
		}
		// PRAGMA takes no bind parameters, and i+1 is not user input.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %s: set user_version: %w", name, step.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%s: %s: commit: %w", name, step.Name, err)
		}
	}
	return nil
}

// unix renders a time for storage: Unix seconds, never text, never milliseconds.
func unix(t time.Time) int64 { return t.UTC().Unix() }

// nullTime maps a zero time to SQL NULL, for the columns where absence is the point:
// done_at, disabled_at, last_nudged_at.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return unix(t)
}

// timeFrom reads a nullable timestamp column back.
func timeFrom(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.Unix(v.Int64, 0).UTC()
}

// NotFound, Conflict and Invalid build a classified error whose message is written for the
// person who will read it in the interface.
//
// Built this way rather than as fmt.Errorf("%w: ...", ErrNotFound), which renders as
// "not found: no reminder r_1" wherever it is shown — and these are shown.
func NotFound(format string, a ...any) error {
	return classified{ErrNotFound, fmt.Sprintf(format, a...)}
}
func Conflict(format string, a ...any) error {
	return classified{ErrConflict, fmt.Sprintf(format, a...)}
}
func Invalid(format string, a ...any) error { return classified{ErrInvalid, fmt.Sprintf(format, a...)} }

type classified struct {
	kind error
	msg  string
}

func (c classified) Error() string { return c.msg }
func (c classified) Unwrap() error { return c.kind }
