package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"btw/internal/config"
	"btw/internal/store"
)

// agent stands in for backio-agent, reading the multipart upload the way it does.
type agent struct {
	mu       sync.Mutex
	archives [][]byte
	names    []string
	status   int
	answer   string
	received chan struct{}
}

func newAgent(t *testing.T) (*httptest.Server, *agent) {
	t.Helper()
	a := &agent{status: http.StatusOK, received: make(chan struct{}, 32)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("the agent was sent %s, want POST", r.Method)
		}
		mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("Content-Type is %q, want multipart", mediaType)
		}

		var body []byte
		var name string
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			switch part.FormName() {
			case "backup":
				body, _ = io.ReadAll(part)
				if name == "" {
					name = part.FileName()
				}
			case "name":
				v, _ := io.ReadAll(part)
				name = string(v)
			}
		}

		a.mu.Lock()
		if body != nil {
			a.archives = append(a.archives, body)
			a.names = append(a.names, name)
		}
		status, answer := a.status, a.answer
		a.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if answer == "" {
			answer = `{"status":"ok","destination":"gdrive:btw/production/btw-20260903_041500.tgz"}`
		}
		fmt.Fprint(w, answer)

		select {
		case a.received <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(srv.Close)
	return srv, a
}

func (a *agent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.archives)
}

func (a *agent) last() ([]byte, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.archives) == 0 {
		return nil, ""
	}
	return a.archives[len(a.archives)-1], a.names[len(a.names)-1]
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var written atomic.Int64

// touch writes something a person typed, which is what main.db is for.
func touch(t *testing.T, st *store.Store) {
	t.Helper()
	// A name of its own each time, so calling this twice in one test is about the write and
	// not about a unique index.
	who, err := st.CreatePrincipal(t.Context(), fmt.Sprintf("somebody%d", written.Add(1)), "a-long-enough-password", store.RoleUser)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}
	if _, err := st.CreateReminder(t.Context(), who.ID, "call the dentist"); err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
}

// members reads an archive back the way a restore would.
func members(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	out := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if h.Typeflag != tar.TypeReg {
			t.Errorf("%s is not a regular file: extracting must not touch a directory that already exists", h.Name)
		}
		// The archive holds every password hash on the instance and the VAPID private key.
		if h.Mode != 0o600 {
			t.Errorf("%s is mode %o, want 600", h.Name, h.Mode)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", h.Name, err)
		}
		out[h.Name] = body
	}
	return out
}

func TestWhatArrivesIsARestorableDatabase(t *testing.T) {
	srv, side := newAgent(t)
	st := testStore(t)

	// Something written through the app, so the copy can be shown to hold it rather than
	// merely to be a database.
	if _, _, err := st.VAPIDKeys(t.Context()); err != nil {
		t.Fatalf("VAPIDKeys(): %v", err)
	}

	p := &Pusher{Store: st, Log: quiet(), URL: srv.URL, Mode: config.BackupMain}
	if err := p.Once(t.Context()); err != nil {
		t.Fatalf("Once(): %v", err)
	}

	body, name := side.last()
	// Timestamped, so the agent can keep a series rather than one file overwritten.
	if !strings.HasPrefix(name, "btw-") || !strings.HasSuffix(name, ".tgz") {
		t.Errorf("posted name is %q", name)
	}

	files := members(t, body)
	main, ok := files[store.MainFile]
	if !ok {
		t.Fatalf("no %s in the archive: %v", store.MainFile, files)
	}

	// Restorable, not merely present: the point of VACUUM INTO over copying the files is that
	// what comes out opens without a WAL beside it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, store.MainFile), main, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open() on the restored copy: %v", err)
	}
	defer restored.Close()

	key, _, err := restored.VAPIDKeys(t.Context())
	if err != nil {
		t.Fatalf("VAPIDKeys() on the restored copy: %v", err)
	}
	original, _, _ := st.VAPIDKeys(t.Context())
	if !key.Equal(original) {
		// The one secret whose loss cannot be repaired by everybody re-registering.
		t.Error("the restored instance has a different VAPID key")
	}
}

func TestWhatEachModeCarries(t *testing.T) {
	for _, tc := range []struct {
		mode config.BackupMode
		want []string
	}{
		{config.BackupMain, []string{store.MainFile}},
		{config.BackupRelaxed, []string{store.MainFile, store.DerivedFile}},
		{config.BackupAll, []string{store.MainFile, store.DerivedFile}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			srv, side := newAgent(t)
			p := &Pusher{Store: testStore(t), Log: quiet(), URL: srv.URL, Mode: tc.mode}
			if err := p.Once(t.Context()); err != nil {
				t.Fatalf("Once(): %v", err)
			}
			body, _ := side.last()
			files := members(t, body)
			if len(files) != len(tc.want) {
				t.Fatalf("archive holds %d files, want %d", len(files), len(tc.want))
			}
			for _, name := range tc.want {
				if _, ok := files[name]; !ok {
					t.Errorf("no %s in the archive", name)
				}
			}
		})
	}
}

func TestAnUnchangedDatabaseIsNotSentTwice(t *testing.T) {
	srv, side := newAgent(t)
	st := testStore(t)
	p := &Pusher{Store: st, Log: quiet(), URL: srv.URL, Mode: config.BackupRelaxed}

	// The first pass always sends: nothing has ever been copied.
	if err := p.Once(t.Context()); err != nil {
		t.Fatalf("Once() #1: %v", err)
	}
	if side.count() != 1 {
		t.Fatalf("the first pass sent %d archives, want 1", side.count())
	}

	// Nothing typed in between, so there is nothing worth the postage. This is the whole
	// reason the decision moved inside btw: a timer outside cannot know this.
	for i := range 3 {
		if err := p.Once(t.Context()); err != nil {
			t.Fatalf("Once() #%d: %v", i+2, err)
		}
	}
	if side.count() != 1 {
		t.Errorf("an unchanged database was sent %d times", side.count())
	}
}

func TestWritingSomethingDownEarnsACopy(t *testing.T) {
	srv, side := newAgent(t)
	st := testStore(t)
	p := &Pusher{Store: st, Log: quiet(), URL: srv.URL, Mode: config.BackupRelaxed}

	if err := p.Once(t.Context()); err != nil {
		t.Fatalf("Once() #1: %v", err)
	}
	touch(t, st)
	if err := p.Once(t.Context()); err != nil {
		t.Fatalf("Once() #2: %v", err)
	}
	if side.count() != 2 {
		t.Fatalf("a new reminder produced %d archives, want 2", side.count())
	}

	// And the copy that went out holds it.
	body, _ := side.last()
	files := members(t, body)
	if !bytes.Contains(files[store.MainFile], []byte("call the dentist")) {
		t.Error("the archive does not carry the reminder that triggered it")
	}
}

func TestOnlyAllSendsWhenNothingWasTyped(t *testing.T) {
	for _, tc := range []struct {
		mode config.BackupMode
		want int
	}{
		{config.BackupMain, 1},
		{config.BackupRelaxed, 1},
		{config.BackupAll, 2},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			srv, side := newAgent(t)
			st := testStore(t)
			p := &Pusher{Store: st, Log: quiet(), URL: srv.URL, Mode: tc.mode}
			if err := p.Once(t.Context()); err != nil {
				t.Fatalf("Once(): %v", err)
			}

			// Backdate the last push past every floor, without touching main.db. Only a mode
			// with a floor should care.
			digest, _, err := st.LastBackup(t.Context())
			if err != nil {
				t.Fatalf("LastBackup(): %v", err)
			}
			stale := time.Now().Add(-config.BackupAllPeriod - time.Minute)
			if err := st.RecordBackup(t.Context(), digest, stale); err != nil {
				t.Fatalf("RecordBackup(): %v", err)
			}

			if err := p.Once(t.Context()); err != nil {
				t.Fatalf("Once() after the floor: %v", err)
			}
			if side.count() != tc.want {
				t.Errorf("%s sent %d archives, want %d", tc.mode, side.count(), tc.want)
			}
		})
	}
}

func TestARejectedUploadIsNotRemembered(t *testing.T) {
	srv, side := newAgent(t)
	side.status = http.StatusForbidden
	side.answer = `{"error":true,"message":"token lacks permission for gdrive:btw/production"}`

	st := testStore(t)
	p := &Pusher{Store: st, Log: quiet(), URL: srv.URL, Mode: config.BackupMain}

	err := p.Once(t.Context())
	if err == nil {
		t.Fatal("Once() reported success on a 403")
	}
	// The agent's own words are worth more in a log than the status they arrived with.
	if !strings.Contains(err.Error(), "token lacks permission") {
		t.Errorf("the refusal lost the agent's answer: %v", err)
	}

	// Nothing may be recorded, or btw would believe a copy exists that does not — and with
	// nothing else changing, the next write would be the only thing that ever tried again.
	digest, at, err := st.LastBackup(t.Context())
	if err != nil {
		t.Fatalf("LastBackup(): %v", err)
	}
	if digest != nil || !at.IsZero() {
		t.Error("a refused upload was recorded as a backup")
	}

	// So the next pass tries the same copy again rather than waiting for a change.
	side.mu.Lock()
	side.status, side.answer = http.StatusOK, ""
	side.mu.Unlock()
	if err := p.Once(t.Context()); err != nil {
		t.Fatalf("Once() after the agent recovered: %v", err)
	}
	if side.count() != 2 {
		t.Errorf("the agent saw %d attempts, want 2", side.count())
	}
}

func TestAnAgentThatIsNotThereIsAnError(t *testing.T) {
	// Nothing listening is the ordinary case of an agent that has not started yet, and it must
	// not look like a backup that happened.
	p := &Pusher{Store: testStore(t), Log: quiet(), URL: "http://127.0.0.1:1/backup", Mode: config.BackupMain}
	if err := p.Once(t.Context()); err == nil {
		t.Fatal("Once() reported success with nothing listening")
	}
}

func TestNoAgentMeansNoWork(t *testing.T) {
	// Run must return rather than spin: with nowhere to send, there is nothing to do.
	p := &Pusher{Store: testStore(t), Log: quiet(), Mode: config.BackupRelaxed}
	done := make(chan struct{})
	go func() { p.Run(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() kept working with no URL")
	}
}

func TestRunCopiesAtOnceAndKeepsLooking(t *testing.T) {
	srv, side := newAgent(t)
	st := testStore(t)
	p := &Pusher{Store: st, Log: quiet(), URL: srv.URL, Mode: config.BackupRelaxed, Every: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go p.Run(ctx)

	// The first pass is immediate: a process that just started may be on a new volume, and
	// waiting five minutes to find that out is five minutes nobody has a copy of.
	select {
	case <-side.received:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent at startup")
	}

	// It keeps looking, and sends again once something is written.
	touch(t, st)
	select {
	case <-side.received:
	case <-time.After(5 * time.Second):
		t.Fatal("a change after startup was never copied")
	}
}

func TestRunSurvivesAnAgentThatRefuses(t *testing.T) {
	srv, side := newAgent(t)
	side.status = http.StatusInternalServerError
	p := &Pusher{Store: testStore(t), Log: quiet(), URL: srv.URL, Mode: config.BackupMain, Every: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go p.Run(ctx)

	// An agent that is down is not a reason to stop trying, nor to stop serving reminders.
	for range 2 {
		select {
		case <-side.received:
		case <-time.After(5 * time.Second):
			t.Fatal("the loop gave up after a failure")
		}
	}
}

func TestRunStopsWithTheContext(t *testing.T) {
	srv, _ := newAgent(t)
	p := &Pusher{Store: testStore(t), Log: quiet(), URL: srv.URL, Mode: config.BackupMain, Every: time.Hour}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() outlived its context")
	}
}

func TestTheDigestIsMainAloneSoDerivedDoesNotDriveIt(t *testing.T) {
	// In a mode that carries derived.db, the recorded digest must still be main.db's: taken
	// from the whole archive, it would never match the probe and every pass would send.
	st := testStore(t)
	now := time.Now()

	main, err := Build(t.Context(), st, false, now)
	if err != nil {
		t.Fatalf("Build(main): %v", err)
	}
	both, err := Build(t.Context(), st, true, now)
	if err != nil {
		t.Fatalf("Build(both): %v", err)
	}
	if !bytes.Equal(main.Digest, both.Digest) {
		t.Error("the digest changes with what the archive carries, so an idle instance would upload forever")
	}
	if len(both.Body) <= len(main.Body) {
		t.Error("carrying derived.db did not make the archive bigger")
	}
}
