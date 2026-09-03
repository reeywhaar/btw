package backup

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"

	"btw/internal/config"
	"btw/internal/store"
)

// pushTimeout bounds one pass, end to end.
//
// Generous, because it covers building the archive as well as sending it: `VACUUM INTO` on a
// large instance takes a moment, and the upload leaves this machine. A backup cut short would
// fail exactly on the instances that most need one.
const pushTimeout = 10 * time.Minute

// Pusher copies the databases to a backup agent, on the terms the mode sets.
type Pusher struct {
	Store *store.Store
	Log   *slog.Logger

	// URL is where an agent takes an archive. Empty means nothing is sent at all.
	URL  string
	Mode config.BackupMode

	// Every is how often to look, and defaults to [config.BackupDelay].
	//
	// A field only so a test can drive the loop without waiting five minutes for it. Nothing
	// reads it from the environment: see the constants in config for why neither timing here
	// is a setting.
	Every time.Duration

	Client *http.Client
}

// Run works until the context is done.
//
// The first pass is immediate rather than one interval in. A process that has just started is
// the one most likely to have been restarted onto a new volume, or to be running a version
// whose migrations have just rewritten main.db, and waiting to find that out is time spent in
// a state nobody has a copy of.
//
// **The interval is also the throttle**, and that is most of what it is for. Somebody writing
// down six things they mean to do writes to main.db six times in a minute; looking on a timer
// rather than on every write turns that into one archive, five minutes later, holding all six.
// Nothing here reacts to a write directly, so there is no burst it can be made to keep up with.
func (p *Pusher) Run(ctx context.Context) {
	if p.URL == "" {
		return
	}
	if p.Every <= 0 {
		p.Every = config.BackupDelay
	}
	p.Log.Info("backing up", "to", p.URL, "mode", p.Mode, "after_a_change_within", p.Every,
		"carries_derived", p.Mode.Derived(), "at_least_every", p.Mode.Period())

	for {
		if err := p.Once(ctx); err != nil {
			// Logged and left for the next pass rather than retried here. What fails is
			// either transient — the agent restarting, the remote unreachable — or needs a
			// person, and neither is helped by trying again a second later.
			p.Log.Error("backup failed", "error", err)
		}

		timer := time.NewTimer(p.Every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// client is the one this was given, or one of our own.
//
// Defaulted here rather than in [Pusher.Run], because Once is reachable without it — a test
// drives a single pass, and a caller that did the same against a nil client got a segfault
// rather than a backup.
func (p *Pusher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	p.Client = &http.Client{Timeout: pushTimeout}
	return p.Client
}

// Once builds a copy, sends it if the mode says it is due, and remembers what was sent.
//
// Exported so a test can drive one pass without waiting on a clock.
func (p *Pusher) Once(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()

	now := time.Now()
	last, at, err := p.Store.LastBackup(ctx)
	if err != nil {
		return err
	}

	// main.db alone is snapshotted first, and only to be hashed.
	//
	// The question is what is *in* main.db, and there is nothing cheaper that answers it: an
	// mtime moves for a write that changed nothing, and a page count is shared by databases
	// that differ. `VACUUM INTO` is the read either way. Doing it on main alone means an
	// instance carrying derived.db is not rebuilding it every five minutes to find out whether
	// anybody touched a reminder.
	probe, err := Build(ctx, p.Store, false, now)
	if err != nil {
		return err
	}
	changed := !bytes.Equal(last, probe.Digest)

	// And the floor, which only "all" has. It is a heartbeat rather than a guard on anything:
	// an agent told how often to expect an archive can say a btw has stopped backing up, and
	// with copies arriving only when somebody writes something down it cannot tell a broken
	// instance from a quiet one.
	//
	// A push clears both at once. The digest it records is the one the probe just took, so the
	// change that was waiting for its delay has been sent — there is nothing left pending, and
	// the floor starts again from now.
	period := p.Mode.Period()
	due := period > 0 && !at.IsZero() && now.Sub(at) >= period

	if !changed && !due {
		p.Log.Debug("nothing to back up; main.db is as it was",
			"since", at.Format(time.RFC3339), "floor", period)
		return nil
	}

	archive, err := Build(ctx, p.Store, p.Mode.Derived(), now)
	if err != nil {
		return err
	}

	name := Filename(now)
	if err := Push(ctx, p.client(), p.URL, name, archive.Body); err != nil {
		return err
	}

	// Only now. Recorded before the agent accepted it, a rejected upload would leave btw
	// believing a copy exists that does not — and in the change-driven modes the next write to
	// main.db would be the only thing that ever made it try again.
	if err := p.Store.RecordBackup(ctx, archive.Digest, now); err != nil {
		return err
	}

	p.Log.Info("backed up", "name", name, "bytes", len(archive.Body),
		"mode", p.Mode, "changed", changed, "first", at.IsZero())
	return nil
}
