package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"btw/internal/config"
	"btw/internal/store"
	"btw/internal/webpush"
)

// fakeNudger stands in for the scheduler, so the button can be tested without a push
// service on the other end.
type fakeNudger struct {
	called  bool
	outcome string
}

func (f *fakeNudger) NudgeNow(context.Context, string) (string, error) {
	f.called = true
	if f.outcome == "" {
		return "nothing", nil
	}
	return f.outcome, nil
}

type harness struct {
	*testing.T
	srv    *httptest.Server
	store  *store.Store
	nudger *fakeNudger
	cookie string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { st.Close() })

	key, pub, err := st.VAPIDKeys(t.Context())
	if err != nil {
		t.Fatalf("VAPIDKeys(): %v", err)
	}
	cfg := &config.Config{
		PublicURL: mustURL(t, "https://btw.example.com"),
		Secure:    true,
	}
	// A bundle, so the SPA paths are exercised rather than always falling to the
	// placeholder.
	spa, err := NewSPA(fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>app</title>")},
		"login.html":    {Data: []byte("<!doctype html><title>login</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})
	if err != nil {
		t.Fatalf("NewSPA(): %v", err)
	}

	h := &harness{T: t, store: st, nudger: &fakeNudger{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(cfg, st, log, webpush.NewSender(key, pub, "https://btw.example.com"), h.nudger, spa)
	h.srv = httptest.NewServer(server.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// signIn creates an account and holds its session cookie for later requests.
func (h *harness) signIn() store.Principal {
	h.Helper()
	p, err := h.store.CreatePrincipal(h.Context(), "misha", "a-good-password", store.RoleAdmin)
	if err != nil {
		h.Fatalf("CreatePrincipal(): %v", err)
	}
	resp := h.do("POST", "/api/auth/login", map[string]string{"username": "misha", "password": "a-good-password"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		h.Fatalf("login = %s, want 204", resp.Status)
	}
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			h.cookie = c.Value
		}
	}
	if h.cookie == "" {
		h.Fatal("login set no session cookie")
	}
	return p
}

func (h *harness) do(method, path string, body any) *http.Response {
	h.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, r)
	if err != nil {
		h.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// What a browser sends for a fetch from this origin, and what a service worker sends
	// too — which is why the notification buttons need no exception in the guard.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if h.cookie != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: h.cookie})
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func TestNoCORSHeaderIsEverEmitted(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	// The absence is load-bearing rather than an oversight: the browser only ever talks to
	// this origin, and Access-Control-Allow-Origin would weaken two of the three CSRF
	// defences that replace it.
	for _, path := range []string{"/healthz", "/api/me", "/api/reminders", "/api/nope", "/"} {
		resp := h.do("GET", path, nil)
		got := resp.Header.Get("Access-Control-Allow-Origin")
		resp.Body.Close()
		if got != "" {
			t.Errorf("%s emitted Access-Control-Allow-Origin: %q", path, got)
		}
	}
}

func TestWithoutASessionEverythingIsRefusedAsJSON(t *testing.T) {
	h := newHarness(t)
	resp := h.do("GET", "/api/reminders", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %s, want 401", resp.Status)
	}
	// Never a redirect: a 302 to an HTML page is the least useful thing a fetch can get.
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

func TestAMistypedAPIPathIsJSONNotTheShell(t *testing.T) {
	h := newHarness(t)
	resp := h.do("GET", "/api/remindrs", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %s, want 404", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON — an HTML 404 reaches fetch as a parse error", ct)
	}
}

func TestACrossSiteMutationIsRefused(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	req, _ := http.NewRequest("POST", h.srv.URL+"/api/reminders", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: h.cookie})

	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %s, want 403", resp.Status)
	}
}

func TestAMutationMustDeclareJSON(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	req, _ := http.NewRequest("POST", h.srv.URL+"/api/reminders", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: h.cookie})

	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %s, want 415", resp.Status)
	}
}

func TestWriteOneDownAndReadItBack(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	resp := h.do("POST", "/api/reminders", map[string]string{"text": "go to the circus"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %s, want 201", resp.Status)
	}
	var created struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	decodeBody(t, resp, &created)
	if created.Text != "go to the circus" {
		t.Errorf("text = %q", created.Text)
	}

	var list struct {
		Reminders []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"reminders"`
	}
	decodeBody(t, h.do("GET", "/api/reminders", nil), &list)
	if len(list.Reminders) != 1 || list.Reminders[0].ID != created.ID {
		t.Fatalf("list = %+v, want the one just written", list.Reminders)
	}
}

func TestEndingAReminderTakesItOffTheList(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	var created struct {
		ID string `json:"id"`
	}
	decodeBody(t, h.do("POST", "/api/reminders", map[string]string{"text": "ring the dentist"}), &created)

	resp := h.do("POST", "/api/reminders/"+created.ID+"/done", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("done = %s, want 204", resp.Status)
	}

	var live struct {
		Reminders []json.RawMessage `json:"reminders"`
	}
	decodeBody(t, h.do("GET", "/api/reminders", nil), &live)
	if len(live.Reminders) != 0 {
		t.Errorf("live list still holds %d", len(live.Reminders))
	}

	var done struct {
		Reminders []json.RawMessage `json:"reminders"`
	}
	decodeBody(t, h.do("GET", "/api/reminders?done=true", nil), &done)
	if len(done.Reminders) != 1 {
		t.Errorf("done list holds %d, want 1", len(done.Reminders))
	}
}

func TestAnswringANudgeEndsItsReminder(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()

	rem, err := h.store.CreateReminder(h.Context(), p.ID, "water the plants")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
	nudgeID := store.NewNudgeID()
	if _, err := h.store.RecordNudge(h.Context(), nudgeID, p.ID, rem.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}

	// This is the request the service worker makes when somebody taps Drop on a lock
	// screen. Same-origin from a worker, so the cookie rides along and the guard passes.
	resp := h.do("POST", "/api/nudges/"+nudgeID+"/drop", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("drop = %s, want 204", resp.Status)
	}

	got, err := h.store.Reminder(h.Context(), p.ID, rem.ID)
	if err != nil {
		t.Fatalf("Reminder(): %v", err)
	}
	if !got.Done() {
		t.Error("the reminder is still live after its nudge was dropped")
	}
}

func TestSomebodyElsesReminderIsNotFoundRatherThanForbidden(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	other, err := h.store.CreatePrincipal(h.Context(), "someone", "a-good-password", store.RoleUser)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}
	theirs, err := h.store.CreateReminder(h.Context(), other.ID, "not yours")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	// Whether a stranger keeps a reminder is not the caller's business either way, and
	// scoping the lookup and checking the owner become one operation.
	resp := h.do("POST", "/api/reminders/"+theirs.ID+"/done", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %s, want 404", resp.Status)
	}
}

func TestTheRhythmNeverSaysWhenTheNextNudgeIs(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	resp := h.do("GET", "/api/rhythm", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// A person who can see that the next nudge is at 14:32 is a person waiting for 14:32,
	// and the surprise is the entire mechanism.
	for _, forbidden := range []string{"next", "slot", "_at"} {
		if bytes.Contains(bytes.ToLower(body), []byte(forbidden)) {
			t.Errorf("the rhythm leaked scheduling detail (%q): %s", forbidden, body)
		}
	}
}

func TestTheTestButtonGoesThroughTheScheduler(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.nudger.outcome = "sent"

	var got struct {
		Sent bool `json:"sent"`
	}
	decodeBody(t, h.do("POST", "/api/nudges", nil), &got)
	if !h.nudger.called {
		t.Error("the button did not reach the scheduler")
	}
	if !got.Sent {
		t.Error("sent = false")
	}
}

func TestNothingEligibleIsNotAnError(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.nudger.outcome = "nothing"

	resp := h.do("POST", "/api/nudges", nil)
	defer resp.Body.Close()
	// "Everything is done or inside its own interval" is a state the interface explains,
	// not a failure of the button.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %s, want 200", resp.Status)
	}
}

func TestTheVAPIDKeyIsPublic(t *testing.T) {
	h := newHarness(t)
	// The page needs it before there is any question of a session, and it is a public key.
	var got struct {
		Key string `json:"key"`
	}
	decodeBody(t, h.do("GET", "/api/push/key", nil), &got)
	if len(got.Key) < 80 {
		t.Errorf("key = %q, want an uncompressed P-256 point", got.Key)
	}
}

func TestADeviceEndpointNeverComesBackOut(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()

	const endpoint = "https://push.example.com/very-secret-capability"
	if _, err := h.store.RegisterDevice(h.Context(), p.ID, endpoint, "k", "s", "phone"); err != nil {
		t.Fatalf("RegisterDevice(): %v", err)
	}

	resp := h.do("GET", "/api/devices", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// The endpoint is a capability: anybody holding it and a VAPID key can put text on
	// that lock screen.
	if bytes.Contains(body, []byte(endpoint)) {
		t.Errorf("the device endpoint reached the client: %s", body)
	}
}

func TestANavigationGetsAShellAndAMissingFileDoesNot(t *testing.T) {
	h := newHarness(t)

	req, _ := http.NewRequest("GET", h.srv.URL+"/login", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte("login")) {
		t.Errorf("/login served %s, want the login shell", body)
	}

	// A deep link to a sub-route gets the app shell, which is what makes the URL usable as
	// an address at all — and what the back gesture on an installed web app depends on.
	req, _ = http.NewRequest("GET", h.srv.URL+"/settings", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	resp, err = h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte("app")) {
		t.Errorf("/settings served %s, want the app shell", body)
	}

	// A missing /app.js served as HTML presents as a MIME-type error with no hint that the
	// file simply is not there.
	req, _ = http.NewRequest("GET", h.srv.URL+"/assets/missing.js", nil)
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	resp, err = h.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset = %s, want 404", resp.Status)
	}
}

// signInAs replaces the harness's session with one for a freshly created account of the
// given role, so the admin gate can be driven from both sides.
func (h *harness) signInAs(username, role string) store.Principal {
	h.Helper()
	p, err := h.store.CreatePrincipal(h.Context(), username, "a-good-password", role)
	if err != nil {
		h.Fatalf("CreatePrincipal(): %v", err)
	}
	h.cookie = ""
	resp := h.do("POST", "/api/auth/login", map[string]string{
		"username": username, "password": "a-good-password",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		h.Fatalf("login as %s = %s", role, resp.Status)
	}
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			h.cookie = c.Value
		}
	}
	return p
}

func TestAdminRoutesAreForAdministrators(t *testing.T) {
	h := newHarness(t)
	h.signInAs("ordinary", store.RoleUser)

	// Every admin route, checked as an ordinary account, because the gate is applied at
	// registration and a route added without it is the failure this guards.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/admin/relay"},
		{"PUT", "/api/admin/relay"},
		{"DELETE", "/api/admin/relay"},
		{"POST", "/api/admin/relay/test"},
	} {
		var body any
		if tc.method == "PUT" || tc.method == "POST" {
			body = map[string]any{}
		}
		resp := h.do(tc.method, tc.path, body)
		resp.Body.Close()
		// 403 rather than 404: an administrator route is not a secret, and "you are signed
		// in, and this is not yours" is the honest answer.
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a user = %s, want 403", tc.method, tc.path, resp.Status)
		}
	}
}

func TestAdminRoutesStillNeedASession(t *testing.T) {
	h := newHarness(t)
	resp := h.do("GET", "/api/admin/relay", nil)
	defer resp.Body.Close()
	// Not 403: without a session there is nobody to refuse, and saying "that is for
	// administrators" to a stranger would say the route exists and they are merely the
	// wrong person.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %s, want 401", resp.Status)
	}
}

func TestARelayPasswordNeverComesBackOut(t *testing.T) {
	h := newHarness(t)
	h.signInAs("admin", store.RoleAdmin)

	saved := h.do("PUT", "/api/admin/relay", map[string]any{
		"host": "smtp.example.com", "port": 587, "tls": "starttls",
		"username": "postmaster", "password": "hunter2",
		"from_address": "btw@example.com", "sender_name": "btw",
	})
	defer saved.Body.Close()
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %s", saved.Status)
	}

	resp := h.do("GET", "/api/admin/relay", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("hunter2")) {
		t.Errorf("the relay password reached the client: %s", body)
	}
	if !bytes.Contains(body, []byte(`"password_set":true`)) {
		t.Errorf("password_set missing, so the form cannot tell whether one is stored: %s", body)
	}
}

func TestSavingWithoutAPasswordKeepsTheStoredOne(t *testing.T) {
	h := newHarness(t)
	h.signInAs("admin", store.RoleAdmin)

	h.do("PUT", "/api/admin/relay", map[string]any{
		"host": "smtp.example.com", "port": 587, "tls": "starttls",
		"username": "postmaster", "password": "hunter2", "from_address": "btw@example.com",
	}).Body.Close()

	// Correcting a port must not mean retyping a credential the form was never given.
	h.do("PUT", "/api/admin/relay", map[string]any{
		"host": "smtp.example.com", "port": 465, "tls": "implicit",
		"username": "postmaster", "password": "", "from_address": "btw@example.com",
	}).Body.Close()

	set, err := h.store.SMTP(h.Context())
	if err != nil {
		t.Fatalf("SMTP(): %v", err)
	}
	if set.Password != "hunter2" {
		t.Errorf("password = %q, want the stored one kept", set.Password)
	}
	if set.Port != 465 {
		t.Errorf("port = %d, want the correction applied", set.Port)
	}
}

func TestARecoveryAddressCannotBeAddedWithoutARelay(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	resp := h.do("POST", "/api/auth/recovery", map[string]string{"email": "misha@example.com"})
	defer resp.Body.Close()
	// Refused before anything is written: an address stored against a relay that does not
	// exist is a promise the product cannot keep.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %s, want 400", resp.Status)
	}

	var got struct {
		Email          string `json:"email"`
		MailConfigured bool   `json:"mail_configured"`
	}
	decodeBody(t, h.do("GET", "/api/auth/recovery", nil), &got)
	if got.Email != "" {
		t.Errorf("an address was recorded anyway: %q", got.Email)
	}
	// Said out loud, because a button disabled without a reason sends somebody looking for
	// it in the wrong place.
	if got.MailConfigured {
		t.Error("mail_configured is true with no relay")
	}
}

func TestARecoveryAddressIsOnlyProvedByItsCode(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()

	// Written straight to the store: the endpoint that would send the code needs a real
	// relay, and what is being tested here is what the confirm step does.
	code, err := h.store.StartRecovery(h.Context(), p.ID, "misha@example.com")
	if err != nil {
		t.Fatalf("StartRecovery(): %v", err)
	}

	var before struct {
		Email   string `json:"email"`
		Pending string `json:"pending"`
	}
	decodeBody(t, h.do("GET", "/api/auth/recovery", nil), &before)
	if before.Email != "" || before.Pending != "misha@example.com" {
		t.Fatalf("before confirming: email=%q pending=%q", before.Email, before.Pending)
	}

	bad := h.do("POST", "/api/auth/recovery/confirm", map[string]string{"code": "00000000"})
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("a wrong code = %s, want 400", bad.Status)
	}

	var confirmed struct {
		Email string `json:"email"`
	}
	decodeBody(t, h.do("POST", "/api/auth/recovery/confirm", map[string]string{"code": code}), &confirmed)
	if confirmed.Email != "misha@example.com" {
		t.Fatalf("confirm returned %q", confirmed.Email)
	}

	resp := h.do("DELETE", "/api/auth/recovery", nil)
	resp.Body.Close()
	var after struct {
		Email string `json:"email"`
	}
	decodeBody(t, h.do("GET", "/api/auth/recovery", nil), &after)
	if after.Email != "" {
		t.Errorf("the address survived being forgotten: %q", after.Email)
	}
}

func TestChangingAPasswordKeepsThisSessionAndEndsTheOthers(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	here := h.cookie

	// A second device, signed in with the same account.
	elsewhere := store.NewSessionToken()
	if err := h.store.CreateSession(h.Context(), elsewhere, p.ID); err != nil {
		t.Fatalf("CreateSession(): %v", err)
	}

	resp := h.do("POST", "/api/auth/password", map[string]string{
		"current_password": "a-good-password",
		"new_password":     "a-better-password",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %s, want 204", resp.Status)
	}

	// Signing somebody out of the tab they are typing in would be a strange way to confirm
	// it worked — but the token is new, so the credential rotates at the moment somebody is
	// worried enough about it to be here.
	var reissued string
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			reissued = c.Value
		}
	}
	if reissued == "" {
		t.Fatal("no session was re-issued")
	}
	if reissued == here {
		t.Error("the same token came back; a password change should rotate it")
	}
	if _, _, err := h.store.Session(h.Context(), reissued); err != nil {
		t.Errorf("the re-issued session does not resolve: %v", err)
	}
	if _, _, err := h.store.Session(h.Context(), elsewhere); err == nil {
		t.Error("the other device is still signed in")
	}

	// And the password actually changed.
	if _, err := h.store.Authenticate(h.Context(), "misha", "a-better-password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

func TestChangingAPasswordNeedsTheCurrentOne(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	resp := h.do("POST", "/api/auth/password", map[string]string{
		"current_password": "not-it",
		"new_password":     "a-better-password",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %s, want 400", resp.Status)
	}
	// Being signed in is not the same as knowing it: a borrowed session must not be a way
	// to take the account.
	if _, err := h.store.Authenticate(h.Context(), "misha", "a-good-password"); err != nil {
		t.Error("the old password stopped working after a refused change")
	}
}
