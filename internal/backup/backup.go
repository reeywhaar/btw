// Package backup takes a copy of the databases and hands it to somewhere that keeps copies.
//
// # Why btw pushes rather than serving the archive
//
// The other arrangement is a listener of btw's own answering GET /backup, with a container
// fetching it on a loop. It costs an image to build, publish and keep, and it puts a way *into*
// the databases on the network — one that has to be either protected by a credential or left
// unauthenticated behind a closed port.
//
// Worse, the loop could only ever be a timer. A thing outside this process cannot know whether
// anything has been written since the last copy, so it must either copy on a schedule and send
// a week of identical archives, or not copy often enough.
//
// Pushing fixes both. The image is somebody else's — a backup agent takes an archive at POST
// /backup and handles encryption, naming, upload and retention — and the decision of *when*
// moves in here, where the answer is knowable. btw is left holding no credential and no idea
// where its backups end up, so a compromised container cannot reach a single existing backup.
// It can only hand over one more archive.
//
// # What "changed" means
//
// main.db, and only main.db. That is what somebody typed — accounts, reminders, devices,
// rhythms — plus the VAPID keypair, and it is the file that cannot be reconstructed from
// anywhere. derived.db is sessions, the nudge waiting to go out and the nudge log; it travels
// *in* the archive when the operator asks for it, but it does not decide whether one is made.
//
// Nor the columns btw stamps into main.db itself as it delivers a nudge — see
// [store.Store.MainDigest]. A nudge is not something anybody wrote down, and counting it sent
// an archive after every one.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"btw/internal/app"
	"btw/internal/store"
)

// Filename is the archive's name, timestamped so a backup store can keep a series.
func Filename(t time.Time) string {
	return app.Name + "-" + t.UTC().Format("20060102_150405") + ".tgz"
}

// Build renders the databases as a gzipped tar, in memory.
//
// A tar header needs its entry's size up front, so the databases have to be in hand anyway.
// See [store.Store.Snapshot] for why this is `VACUUM INTO` rather than a copy of the files.
func Build(ctx context.Context, st *store.Store, derived bool, now time.Time) ([]byte, error) {
	files, err := st.Snapshot(ctx, derived)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}

	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		// 0600 throughout: the archive carries every password hash on the instance and the
		// VAPID private key, so an extracted copy should not be readable by anyone else. No
		// directory entries, so extracting cannot re-chmod a data directory that already
		// exists.
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     f.Name,
			Mode:     0o600,
			Size:     int64(len(f.Data)),
			ModTime:  now.UTC().Truncate(time.Second),
		}); err != nil {
			return nil, fmt.Errorf("write %s header: %w", f.Name, err)
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, fmt.Errorf("write %s: %w", f.Name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	return out.Bytes(), nil
}

// maxReply is how much of a rejection is read back.
//
// Enough to carry a sentence from the other end and not enough for a server answering with a
// page of HTML to put a page of HTML in the log.
const maxReply = 2 << 10

// Push posts one archive to a backup agent.
//
// Multipart, with the file under `backup` and the name beside it, which is what the agent
// documents. It is a POST rather than a PUT because the agent keeps a series: each one is a
// new archive rather than a replacement for the last.
func Push(ctx context.Context, client *http.Client, url, name string, body []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("backup", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(body); err != nil {
		return err
	}
	if err := w.WriteField("name", name); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// What it said, not just that it said no. "The agent answered 500" is a fact nobody
		// can act on; the body underneath is where the rejected token and the unreachable
		// remote live.
		said, _ := io.ReadAll(io.LimitReader(res.Body, maxReply))
		return fmt.Errorf("the backup agent answered %s: %s", res.Status, bytes.TrimSpace(said))
	}
	return nil
}
