package nudge

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"btw/internal/store"
	"btw/internal/webpush"
)

var b64 = base64.RawURLEncoding

// pushService stands in for Firebase or Mozilla: it records what arrived and, because the
// test holds the user agent's private key, can decrypt it.
//
// This is the closest thing to the real chain that runs without a phone in it. Everything
// between "a slot came due" and "the browser has plaintext" is exercised: selection,
// the nudge id, RFC 8291 encryption, VAPID, the headers, and the recording afterwards.
type pushService struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	status   int
}

func (p *pushService) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.requests = append(p.requests, r.Clone(r.Context()))
		p.bodies = append(p.bodies, body)
		status := p.status
		p.mu.Unlock()

		if status == 0 {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
	}
}

func (p *pushService) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

type rig struct {
	*testing.T
	store     *store.Store
	scheduler *Scheduler
	service   *pushService
	principal store.Principal
	uaPrivate *ecdh.PrivateKey
	uaPublic  []byte
	auth      []byte
}

func newRig(t *testing.T) *rig {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { st.Close() })

	p, err := st.CreatePrincipal(t.Context(), "misha", "a-good-password", store.RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}

	service := &pushService{}
	srv := httptest.NewTLSServer(service.handler())
	t.Cleanup(srv.Close)

	key, pub, err := st.VAPIDKeys(t.Context())
	if err != nil {
		t.Fatalf("VAPIDKeys(): %v", err)
	}
	sender := webpush.NewSender(key, pub, "https://btw.example.com")
	// The test server's certificate is its own, so the sender is pointed at a client that
	// trusts it. Nothing else about the request path changes.
	sender.SetClient(srv.Client())

	// A real subscription keypair, so what the service receives can actually be decrypted.
	uaPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription key: %v", err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)

	r := &rig{
		T:         t,
		store:     st,
		service:   service,
		principal: p,
		uaPrivate: uaPrivate,
		uaPublic:  uaPrivate.PublicKey().Bytes(),
		auth:      auth,
		scheduler: New(st, sender, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	if _, err := st.RegisterDevice(t.Context(), p.ID, srv.URL, b64.EncodeToString(r.uaPublic),
		b64.EncodeToString(auth), "the test phone", "c_test"); err != nil {
		t.Fatalf("RegisterDevice(): %v", err)
	}
	return r
}

// decrypt is the browser's half: parse the aes128gcm header, run the key agreement from
// the user agent's side, and open the record.
func (r *rig) decrypt(body []byte) map[string]string {
	r.Helper()

	salt := body[:16]
	idLen := int(body[20])
	asPublicRaw := body[21 : 21+idLen]
	ciphertext := body[21+idLen:]

	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		r.Fatalf("application server key: %v", err)
	}
	shared, err := r.uaPrivate.ECDH(asPublic)
	if err != nil {
		r.Fatalf("key agreement: %v", err)
	}

	keyInfo := append([]byte("WebPush: info\x00"), r.uaPublic...)
	keyInfo = append(keyInfo, asPublicRaw...)
	ikm, err := hkdf.Key(sha256.New, shared, r.auth, string(keyInfo), 32)
	if err != nil {
		r.Fatalf("derive: %v", err)
	}
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		r.Fatalf("aes: %v", err)
	}
	gcm, _ := cipher.NewGCM(block)
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		r.Fatalf("decrypt: %v", err)
	}

	var out map[string]string
	if err := json.Unmarshal(bytes.TrimRight(record, "\x02"), &out); err != nil {
		r.Fatalf("payload is not the JSON we sent: %v", err)
	}
	return out
}

func TestAReminderArrivesReadableOnTheDevice(t *testing.T) {
	r := newRig(t)
	if _, err := r.store.CreateReminder(t.Context(), r.principal.ID, "go to the circus"); err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	if got, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID); err != nil || got != Sent {
		t.Fatalf("NudgeNow() = %q, %v; want %q", got, err, Sent)
	}
	if r.service.count() != 1 {
		t.Fatalf("the push service got %d requests, want 1", r.service.count())
	}

	got := r.decrypt(r.service.bodies[0])
	if got["text"] != "go to the circus" {
		t.Errorf("text = %q, want the reminder", got["text"])
	}
	// The id has to travel inside the payload: it is what the notification's buttons post
	// back to, and it must name this delivery rather than the reminder.
	if got["nudge_id"] == "" {
		t.Error("the payload carries no nudge id; the buttons would have nothing to answer")
	}

	// And the request itself says the things that make a nudge timely rather than stale.
	req := r.service.requests[0]
	if req.Header.Get("TTL") != "3600" {
		t.Errorf("TTL = %q", req.Header.Get("TTL"))
	}
	if req.Header.Get("Topic") != "btw" {
		t.Errorf("Topic = %q", req.Header.Get("Topic"))
	}
	if auth := req.Header.Get("Authorization"); len(auth) < 20 || auth[:8] != "vapid t=" {
		t.Errorf("Authorization = %q, want the VAPID single-header form", auth)
	}
}

func TestTheNudgeAnswersToItsOwnId(t *testing.T) {
	r := newRig(t)
	rem, _ := r.store.CreateReminder(t.Context(), r.principal.ID, "ring the dentist")

	if _, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID); err != nil {
		t.Fatalf("NudgeNow(): %v", err)
	}
	id := r.decrypt(r.service.bodies[0])["nudge_id"]

	// This is what the service worker does when somebody taps Drop.
	got, err := r.store.ActOnNudge(t.Context(), r.principal.ID, id, store.ActionDrop)
	if err != nil {
		t.Fatalf("ActOnNudge(): %v", err)
	}
	if got != rem.ID {
		t.Fatalf("the nudge answered for %q, want %q", got, rem.ID)
	}
}

func TestNothingEligibleSendsNothingAtAll(t *testing.T) {
	r := newRig(t)
	// No reminders. Reaching for something to send anyway is how a notification channel
	// gets turned off for good.
	got, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID)
	if err != nil {
		t.Fatalf("NudgeNow(): %v", err)
	}
	if got != NothingToSend || r.service.count() != 0 {
		t.Fatalf("outcome=%q, requests=%d; want %q and nothing to leave the process", got, r.service.count(), NothingToSend)
	}
}

func TestARemindersFloorIsNotSpentOnAMessageThatNeverLeft(t *testing.T) {
	r := newRig(t)
	r.service.status = http.StatusInternalServerError
	if _, err := r.store.CreateReminder(t.Context(), r.principal.ID, "water the plants"); err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	got, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID)
	if err != nil {
		t.Fatalf("NudgeNow(): %v", err)
	}
	// Not "nothing to send": something was chosen, and no push service would take it. The
	// two send somebody to different places.
	if got != Undelivered {
		t.Fatalf("outcome = %q, want %q", got, Undelivered)
	}
	// Nothing was delivered, so nothing was raised: the reminder keeps its place rather
	// than going quiet for a day over a notification nobody got.
	if got, _ := r.store.Candidates(t.Context(), r.principal.ID, r.store.Now(), store.RespectFloor); len(got) != 1 {
		t.Errorf("the reminder is no longer eligible; its floor was spent on a failed send")
	}
}

func TestADeadSubscriptionIsForgotten(t *testing.T) {
	r := newRig(t)
	r.service.status = http.StatusGone
	r.store.CreateReminder(t.Context(), r.principal.ID, "buy milk")

	if _, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID); err != nil {
		t.Fatalf("NudgeNow(): %v", err)
	}
	// A browser that has been reinstalled leaves an endpoint that refuses forever, and a
	// device list full of dead rows is how somebody concludes the product is broken.
	devices, _ := r.store.Devices(t.Context(), r.principal.ID)
	if len(devices) != 0 {
		t.Errorf("the gone device is still listed: %d", len(devices))
	}
}

func TestABusySubscriptionIsKept(t *testing.T) {
	r := newRig(t)
	r.service.status = http.StatusTooManyRequests
	r.store.CreateReminder(t.Context(), r.principal.ID, "buy milk")

	if _, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID); err != nil {
		t.Fatalf("NudgeNow(): %v", err)
	}
	devices, _ := r.store.Devices(t.Context(), r.principal.ID)
	if len(devices) != 1 {
		t.Fatalf("a busy push service cost somebody their device")
	}
	if devices[0].FailureCount != 1 {
		t.Errorf("failure_count = %d, want 1", devices[0].FailureCount)
	}
}

func TestASlotFiresOnceAndPlansLazily(t *testing.T) {
	r := newRig(t)
	r.store.CreateReminder(t.Context(), r.principal.ID, "go to the circus")

	// Pin the clock inside the waking window so the plan has slots and one of them is due.
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	r.store.SetClock(func() time.Time { return now })

	r.scheduler.pass(t.Context())
	planned, err := r.store.HasPlan(t.Context(), r.principal.ID, "2026-08-29")
	if err != nil || !planned {
		t.Fatalf("HasPlan() = %v, %v; want the day planned on the first pass", planned, err)
	}

	// Walk the day a quarter-hour at a time and count what actually goes out.
	for range 4 * 14 {
		now = now.Add(15 * time.Minute)
		r.scheduler.pass(t.Context())
	}

	sent := r.service.count()
	if sent == 0 {
		t.Fatal("a whole day passed and nothing was sent")
	}
	// The budget is the ceiling and the reminder's own floor is the real limit here: one
	// reminder at a one-day interval can only be raised once.
	if sent > store.DefaultBudget {
		t.Errorf("%d nudges in one day, want at most the budget of %d", sent, store.DefaultBudget)
	}
}

func TestTheButtonSendsSomethingItJustSent(t *testing.T) {
	r := newRig(t)
	if _, err := r.store.CreateReminder(t.Context(), r.principal.ID, "go to the circus"); err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	// Twice in a row, with one reminder whose floor is a whole day. The first press spends
	// the floor; the second must still send, because a button that usually declines proves
	// nothing about the chain it exists to prove.
	for i := range 2 {
		got, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID)
		if err != nil {
			t.Fatalf("NudgeNow() %d: %v", i+1, err)
		}
		if got != Sent {
			t.Fatalf("press %d = %q, want %q", i+1, got, Sent)
		}
	}
	if r.service.count() != 2 {
		t.Errorf("the push service got %d requests, want 2", r.service.count())
	}
}

func TestTheSchedulerStillWaitsOutTheFloor(t *testing.T) {
	r := newRig(t)
	r.store.CreateReminder(t.Context(), r.principal.ID, "go to the circus")

	// The button ignoring the floor must not have taken it away from the scheduler, which
	// is what stops the same thing arriving twice in one morning.
	if _, err := r.scheduler.NudgeNow(t.Context(), r.principal.ID); err != nil {
		t.Fatalf("NudgeNow(): %v", err)
	}
	before := r.service.count()

	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	r.store.SetClock(func() time.Time { return now })
	for range 4 * 12 {
		now = now.Add(15 * time.Minute)
		r.scheduler.pass(t.Context())
	}
	if r.service.count() != before {
		t.Errorf("the scheduler sent %d more inside the reminder's own interval", r.service.count()-before)
	}
}
