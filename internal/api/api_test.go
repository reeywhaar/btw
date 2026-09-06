package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"btw/internal/config"
	"btw/internal/openrouter"
	"btw/internal/store"
	"btw/internal/webpush"
)

// fakeAdviser records what was asked about, so the button can be tested without a model on the
// other end.
type fakeAdviser struct {
	refreshed []string
	err       error
}

func (f *fakeAdviser) Look(_ context.Context, principalID string) error {
	f.refreshed = append(f.refreshed, principalID)
	return f.err
}

// fakeNudger stands in for the scheduler, so the button can be tested without a push
// service on the other end.
type fakeNudger struct {
	called    bool
	outcome   string
	delivered int
}

func (f *fakeNudger) NudgeNow(context.Context, string) (string, int, error) {
	f.called = true
	if f.outcome == "" {
		return "nothing", 0, nil
	}
	return f.outcome, f.delivered, nil
}

type harness struct {
	*testing.T
	srv     *httptest.Server
	store   *store.Store
	nudger  *fakeNudger
	adviser *fakeAdviser
	cookie  string
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

	h := &harness{T: t, store: st, nudger: &fakeNudger{}, adviser: &fakeAdviser{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(cfg, st, log, webpush.NewSender(key, pub, "https://btw.example.com"), h.nudger, h.adviser, spa)
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
	if _, err := h.store.RegisterDevice(h.Context(), p.ID, endpoint, "k", "s", "phone", ""); err != nil {
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
		{"GET", "/api/admin/proxy"},
		{"PUT", "/api/admin/proxy"},
		{"PATCH", "/api/admin/proxy"},
		{"DELETE", "/api/admin/proxy"},
		{"POST", "/api/admin/proxy/test"},
	} {
		var body any
		if tc.method == "PUT" || tc.method == "POST" || tc.method == "PATCH" {
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

func TestADescriptionIsEditedWithoutRetypingTheSentence(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	var made struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	decodeBody(t, h.do("POST", "/api/reminders", map[string]string{"text": "go to the circus"}), &made)
	if made.Note != "" {
		t.Errorf("a new reminder has a note: %q", made.Note)
	}

	// Absent leaves a field alone, so a description can be added without resending the
	// sentence — and the sentence survives it.
	var noted struct {
		Text string `json:"text"`
		Note string `json:"note"`
	}
	decodeBody(t, h.do("PATCH", "/api/reminders/"+made.ID, map[string]string{
		"note": "the one on the common, tickets at the gate",
	}), &noted)
	if noted.Text != "go to the circus" {
		t.Errorf("text = %q, want it untouched", noted.Text)
	}
	if noted.Note != "the one on the common, tickets at the gate" {
		t.Errorf("note = %q", noted.Note)
	}

	// And empty clears it, which is how a description gets deleted.
	var cleared struct {
		Note string `json:"note"`
	}
	decodeBody(t, h.do("PATCH", "/api/reminders/"+made.ID, map[string]string{"note": ""}), &cleared)
	if cleared.Note != "" {
		t.Errorf("note = %q after clearing", cleared.Note)
	}
}

func TestARemindersSentenceCannotBeEmptied(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	var made struct {
		ID string `json:"id"`
	}
	decodeBody(t, h.do("POST", "/api/reminders", map[string]string{"text": "wash dishes"}), &made)

	resp := h.do("PATCH", "/api/reminders/"+made.ID, map[string]string{"text": "   "})
	defer resp.Body.Close()
	// A reminder with nothing in it is a notification with nothing to say.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %s, want 400", resp.Status)
	}
}

func TestSomebodyElsesReminderCannotBeEdited(t *testing.T) {
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

	resp := h.do("PATCH", "/api/reminders/"+theirs.ID, map[string]string{"note": "mine now"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %s, want 404", resp.Status)
	}
	got, _ := h.store.Reminder(h.Context(), other.ID, theirs.ID)
	if got.Note != "" {
		t.Errorf("their reminder was written to: %q", got.Note)
	}
}

func TestACompanionKeyNeverComesBackOut(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	saved := h.do("PUT", "/api/companion", map[string]any{
		"api_key": "sk-or-v1-secret", "model": "minimax/minimax-m3:free", "about": "I sleep late",
	})
	defer saved.Body.Close()
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %s", saved.Status)
	}

	resp := h.do("GET", "/api/companion", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("sk-or-v1-secret")) {
		t.Errorf("the companion key reached the client: %s", body)
	}
	if !bytes.Contains(body, []byte(`"key_set":true`)) {
		t.Errorf("key_set missing, so the form cannot tell whether one is stored: %s", body)
	}
}

// Changing the model or rewriting a description must not mean retyping a credential the form
// was never given — the same rule as the relay's password.
func TestSavingACompanionWithoutAKeyKeepsTheStoredOne(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()

	h.do("PUT", "/api/companion", map[string]any{
		"api_key": "sk-or-v1-secret", "model": "minimax/minimax-m3:free", "about": "I sleep late",
	}).Body.Close()

	h.do("PUT", "/api/companion", map[string]any{
		"api_key": "", "model": "minimax/minimax-m3", "about": "I sleep late and go to bed at four",
	}).Body.Close()

	set, err := h.store.Companion(h.Context(), p.ID)
	if err != nil {
		t.Fatalf("Companion(): %v", err)
	}
	if set.APIKey != "sk-or-v1-secret" {
		t.Errorf("api_key = %q, want the stored one kept", set.APIKey)
	}
	if set.Model != "minimax/minimax-m3" {
		t.Errorf("model = %q, want the correction applied", set.Model)
	}
}

// One account's own, and never the instance's: the key spends its owner's credit and the
// description is about their life.
func TestACompanionIsNotSharedBetweenAccounts(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "sk-or-v1-mine"}).Body.Close()

	h.signInAs("someone-else", store.RoleUser)
	resp := h.do("GET", "/api/companion", nil)
	defer resp.Body.Close()
	var got struct {
		Configured bool `json:"configured"`
		KeySet     bool `json:"key_set"`
	}
	decodeBody(t, resp, &got)
	if got.Configured || got.KeySet {
		t.Error("another account's companion is visible")
	}
}

// The button lives inside the edit dialog, so it must refuse rather than reach for a key that
// is neither in the form nor already stored.
func TestTestingACompanionWithoutAKeyAnywhereIsRefused(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	resp := h.do("POST", "/api/companion/test", map[string]any{"api_key": "", "model": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %s, want 400", resp.Status)
	}
}

// The button sits beside the field, so it has to mean the field. A press that quietly tried
// the stored key instead would teach somebody that the key they are looking at works.
func TestTheKeyInTheDialogIsTriedAndNotTheStoredOne(t *testing.T) {
	var tried string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tried = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":3}}`)
	}))
	defer srv.Close()
	defer openrouter.SetEndpoint(srv.URL)()

	h := newHarness(t)
	p := h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "stored-key"}).Body.Close()

	h.do("POST", "/api/companion/test", map[string]any{
		"api_key": "typed-key", "model": "",
	}).Body.Close()
	if tried != "Bearer typed-key" {
		t.Errorf("tried %q, want the key in the dialog", tried)
	}

	// And trying stores nothing: a key that turns out not to work must not be left behind by
	// having been tested.
	set, err := h.store.Companion(h.Context(), p.ID)
	if err != nil {
		t.Fatalf("Companion(): %v", err)
	}
	if set.APIKey != "stored-key" {
		t.Errorf("stored key = %q, want trying to have changed nothing", set.APIKey)
	}

	// An empty field means the stored one, under the same rule a save follows — which is what
	// lets somebody try a new model against a key they never retyped.
	h.do("POST", "/api/companion/test", map[string]any{"api_key": "", "model": ""}).Body.Close()
	if tried != "Bearer stored-key" {
		t.Errorf("tried %q, want the stored key when the field is empty", tried)
	}
}

func TestForgettingACompanionLeavesNothingBehind(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "k", "about": "I sleep late"}).Body.Close()

	resp := h.do("DELETE", "/api/companion", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %s, want 204", resp.Status)
	}

	// A delete and not a disabled flag: switching this off withdraws a credential and a
	// description of somebody's life, and leaving either behind is not what they asked for.
	set, err := h.store.Companion(h.Context(), p.ID)
	if err != nil {
		t.Fatalf("Companion(): %v", err)
	}
	if set.APIKey != "" || set.About != "" {
		t.Errorf("Companion() = %+v, want nothing left", set)
	}
}

func TestACompanionNeedsASession(t *testing.T) {
	h := newHarness(t)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/companion"},
		{"PUT", "/api/companion"},
		{"DELETE", "/api/companion"},
		{"POST", "/api/companion/test"},
	} {
		resp := h.do(route.method, route.path, map[string]any{})
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %s, want 401", route.method, route.path, resp.Status)
		}
	}
}

// The companion block has to be able to tell the two states somebody most needs apart: a key
// that stopped working, and a companion that simply has little to say.
func TestTheCompanionSaysHowTheLastRoundWent(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "k"}).Body.Close()

	read := func() map[string]any {
		resp := h.do("GET", "/api/companion", nil)
		defer resp.Body.Close()
		var body struct {
			Advice map[string]any `json:"advice"`
		}
		decodeBody(t, resp, &body)
		return body.Advice
	}

	if got := read()["status"]; got != "none" {
		t.Errorf("status = %v, want none before anything has been asked", got)
	}

	rem, err := h.store.CreateReminder(h.Context(), p.ID, "water the plants")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
	other, err := h.store.CreateReminder(h.Context(), p.ID, "call the dentist")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	now := h.store.Now()
	h.store.SetAdvice(h.Context(), []string{rem.ID, other.ID}, map[string]store.Advice{
		rem.ID: {Curve: aWeek()},
	})
	h.store.RecordAdvised(h.Context(), p.ID, now)

	if got := read()["status"]; got != "some" {
		t.Errorf("status = %v, want some when one of two was answered for", got)
	}

	h.store.SetAdvice(h.Context(), []string{rem.ID, other.ID}, map[string]store.Advice{
		rem.ID:   {Curve: aWeek()},
		other.ID: {Curve: aWeek()},
	})
	if got := read()["status"]; got != "all" {
		t.Errorf("status = %v, want all when both were answered for", got)
	}

	// A quota is not a mistake and must not be shown the way a rejected key is.
	h.store.RecordAdviceFailure(h.Context(), p.ID, now, "Rate limit exceeded", true)
	if got := read()["status"]; got != "limited" {
		t.Errorf("status = %v, want limited", got)
	}

	h.store.RecordAdviceFailure(h.Context(), p.ID, now, "the key was rejected", false)
	advice := read()
	if advice["status"] != "failed" {
		t.Errorf("status = %v, want failed", advice["status"])
	}
	if advice["error"] != "the key was rejected" {
		t.Errorf("error = %v, want the gateway's own words", advice["error"])
	}
}

// docs/api_design.md forbids a count in a response body, and this is the payload most tempted
// to carry one: how much of somebody's list has been answered for.
func TestTheCompanionStatusCarriesNoCounts(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "k"}).Body.Close()
	for _, text := range []string{"one", "two", "three"} {
		if _, err := h.store.CreateReminder(h.Context(), p.ID, text); err != nil {
			t.Fatalf("CreateReminder(): %v", err)
		}
	}
	h.store.RecordAdvised(h.Context(), p.ID, h.store.Now())

	resp := h.do("GET", "/api/companion", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Three reminders, none answered for. A payload carrying "3" anywhere is a payload
	// somebody renders.
	for _, forbidden := range []string{`:3`, `"3"`, `"reminders"`, `"answered"`, `"open"`} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Errorf("the companion status carries %s, which is a count: %s", forbidden, body)
		}
	}
}

func TestAProxyTokenNeverComesBackOut(t *testing.T) {
	h := newHarness(t)
	h.signInAs("admin", store.RoleAdmin)

	saved := h.do("PUT", "/api/admin/proxy", map[string]any{
		"kind": "proxio", "url": "https://proxio.example.com", "token": "px_secret",
	})
	defer saved.Body.Close()
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %s", saved.Status)
	}

	resp := h.do("GET", "/api/admin/proxy", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("px_secret")) {
		t.Errorf("the proxy token reached the client: %s", body)
	}
	if !bytes.Contains(body, []byte(`"token_set":true`)) {
		t.Errorf("token_set missing, so the form cannot tell whether one is stored: %s", body)
	}
}

// Correcting an address must not mean retyping a secret nobody can read off the screen — but
// only while it still names the same endpoint, or a proxio token would be carried to a socks
// host it was never meant for.
func TestSavingAProxyWithoutATokenKeepsItOnlyForTheSameEndpoint(t *testing.T) {
	h := newHarness(t)
	h.signInAs("admin", store.RoleAdmin)

	h.do("PUT", "/api/admin/proxy", map[string]any{
		"kind": "proxio", "url": "https://proxio.example.com", "token": "px_secret",
	}).Body.Close()

	h.do("PUT", "/api/admin/proxy", map[string]any{
		"kind": "proxio", "url": "https://proxio.example.com", "token": "",
	}).Body.Close()
	set, err := h.store.Proxy(h.Context())
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if set.Token != "px_secret" {
		t.Errorf("token = %q, want the stored one kept", set.Token)
	}

	// A different endpoint entirely, with no token: refused rather than given the old one.
	resp := h.do("PUT", "/api/admin/proxy", map[string]any{
		"kind": "socks5", "url": "socks5://elsewhere.example.com:1080",
		"username": "misha", "token": "",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %s, want 400 rather than a token carried somewhere else", resp.Status)
	}
	if set, _ := h.store.Proxy(h.Context()); set.URL != "https://proxio.example.com" {
		t.Errorf("proxy = %+v, want the refused save to have changed nothing", set)
	}
}

// Off keeps everything, so switching back on is a press rather than typing a secret again.
func TestAProxyIsSwitchedOffWithoutBeingForgotten(t *testing.T) {
	h := newHarness(t)
	h.signInAs("admin", store.RoleAdmin)
	h.do("PUT", "/api/admin/proxy", map[string]any{
		"kind": "proxio", "url": "https://proxio.example.com", "token": "px_secret",
	}).Body.Close()

	resp := h.do("PATCH", "/api/admin/proxy", map[string]any{"enabled": false})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH = %s", resp.Status)
	}
	var got struct {
		Enabled  bool `json:"enabled"`
		TokenSet bool `json:"token_set"`
	}
	decodeBody(t, resp, &got)
	if got.Enabled {
		t.Error("still on after being switched off")
	}
	if !got.TokenSet {
		t.Error("switching off lost the credential")
	}

	// Saving is how it comes back on.
	h.do("PUT", "/api/admin/proxy", map[string]any{
		"kind": "proxio", "url": "https://proxio.example.com", "token": "",
	}).Body.Close()
	if set, _ := h.store.Proxy(h.Context()); !set.Enabled {
		t.Error("saving did not switch it back on")
	}
}

func TestTestingAProxyBeforeSavingOneIsRefused(t *testing.T) {
	h := newHarness(t)
	h.signInAs("admin", store.RoleAdmin)

	resp := h.do("POST", "/api/admin/proxy/test", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %s, want 400", resp.Status)
	}
}

// The screen shows what the weighting reads, and nothing else — an answer the program would
// ignore is not something to draw.
func TestTheAdviceScreenShowsWhatTheWeightingReads(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "k"}).Body.Close()

	advised, err := h.store.CreateReminder(h.Context(), p.ID, "watch rick and morty")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
	silent, err := h.store.CreateReminder(h.Context(), p.ID, "nothing said about this")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	week := make(store.Curve, store.Days)
	for d := range week {
		week[d] = make([]float64, store.Windows)
	}
	week[5][42] = 0.9
	h.store.SetAdvice(h.Context(), []string{advised.ID}, map[string]store.Advice{
		advised.ID: {Categories: []string{"entertainment"}, Curve: week},
	})

	resp := h.do("GET", "/api/companion/advice", nil)
	defer resp.Body.Close()
	var got struct {
		Reminders []struct {
			ID      string      `json:"id"`
			Text    string      `json:"text"`
			Advised bool        `json:"advised"`
			Curve   [][]float64 `json:"curve"`
		} `json:"reminders"`
		Days    int `json:"days"`
		Windows int `json:"windows"`
	}
	decodeBody(t, resp, &got)

	if got.Days != store.Days || got.Windows != store.Windows {
		t.Errorf("shape = %dx%d, want %dx%d", got.Days, got.Windows, store.Days, store.Windows)
	}
	if len(got.Reminders) != 2 {
		t.Fatalf("reminders = %d, want both", len(got.Reminders))
	}
	for _, r := range got.Reminders {
		switch r.ID {
		case advised.ID:
			if !r.Advised || len(r.Curve) != store.Days || len(r.Curve[5]) != store.Windows {
				t.Errorf("advised = %+v, want the whole week", r.Advised)
			}
			if r.Curve[5][42] != 0.9 {
				t.Errorf("curve = %v, want the value it was given", r.Curve[5][42])
			}
		case silent.ID:
			if r.Advised || r.Curve != nil {
				t.Error("a reminder nothing was said about came back with a curve")
			}
		}
	}
}

// The press waits and comes back with the answer. It used to hand the work to the loop and
// return before anything had happened, which made somebody judge a change they could not see.
func TestAskingAgainWaitsAndAnswersWithTheAdvice(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	if _, err := h.store.CreateReminder(h.Context(), p.ID, "wash dishes"); err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	resp := h.do("POST", "/api/companion/advice/refresh", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200 with the answer", resp.Status)
	}
	var got struct {
		Reminders []struct {
			Text string `json:"text"`
		} `json:"reminders"`
	}
	decodeBody(t, resp, &got)
	if len(got.Reminders) != 1 || got.Reminders[0].Text != "wash dishes" {
		t.Errorf("reminders = %+v, want the advice as it now stands", got.Reminders)
	}

	if len(h.adviser.refreshed) != 1 || h.adviser.refreshed[0] != p.ID {
		t.Errorf("asked about %v, want this account", h.adviser.refreshed)
	}
	// Marked stale first, because a look declines when nothing has changed — right for the
	// loop, and wrong for a press that means "ask anyway".
	if state, _ := h.store.Advice(h.Context(), p.ID); !state.Stale {
		t.Error("the press did not force a fresh look")
	}
}

// It makes an outbound request on the caller's behalf against a quota with fifty a day in it,
// and the screen's own twenty seconds is not a ceiling — it is only the screen being used.
func TestAskingAgainHasACeiling(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	var limited bool
	for range 8 {
		resp := h.do("POST", "/api/companion/advice/refresh", map[string]any{})
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("asking again can be pressed without limit")
	}
}

// What went wrong reaches the screen, rather than a silent nothing after a long wait.
func TestAFailedAskSaysWhyRatherThanAnsweringEmpty(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.adviser.err = errors.New("the key was rejected")

	resp := h.do("POST", "/api/companion/advice/refresh", map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %s, want 502", resp.Status)
	}
	var body struct {
		Error string `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error != "the key was rejected" {
		t.Errorf("error = %q, want the reason", body.Error)
	}
}

// The settings block said "it has an opinion about all your reminders" over a list where every
// one of them said the opposite. Coverage counted entries the companion answered for; the rows
// counted curves the weighting can actually read, and an unreadable curve is one it ignores.
func TestCoverageCountsAdviceTheWeightingCanActuallyUse(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()
	h.do("PUT", "/api/companion", map[string]any{"api_key": "k"}).Body.Close()

	rem, err := h.store.CreateReminder(h.Context(), p.ID, "wash dishes")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	// Answered for, with a curve nothing can read — which is what the model was actually
	// sending when this was noticed.
	h.store.SetAdvice(h.Context(), []string{rem.ID}, map[string]store.Advice{
		rem.ID: {Categories: []string{"chores"}, Shape: "7x24"},
	})
	h.store.RecordAdvised(h.Context(), p.ID, h.store.Now())

	resp := h.do("GET", "/api/companion", nil)
	defer resp.Body.Close()
	var got struct {
		Advice struct {
			Status string `json:"status"`
		} `json:"advice"`
	}
	decodeBody(t, resp, &got)
	if got.Advice.Status != "none" {
		t.Errorf("status = %q, want none — nothing it said can be used", got.Advice.Status)
	}

	// And the row says which shape it refused, rather than leaving somebody to guess.
	list := h.do("GET", "/api/companion/advice", nil)
	defer list.Body.Close()
	var advice struct {
		Reminders []struct {
			Shape string `json:"shape"`
		} `json:"reminders"`
	}
	decodeBody(t, list, &advice)
	if len(advice.Reminders) != 1 || advice.Reminders[0].Shape != "7x24" {
		t.Errorf("reminders = %+v, want the refused shape named", advice.Reminders)
	}
}

// aWeek is a curve of the shape the weighting reads: seven days of forty-eight.
//
// A helper rather than make(store.Curve, store.Windows), which is what these tests said before
// and was forty-eight empty days — invalid, and unnoticed while nothing checked.
func aWeek() store.Curve {
	c := make(store.Curve, store.Days)
	for d := range c {
		c[d] = make([]float64, store.Windows)
	}
	return c
}

// The refusal that used to stand — "for now the waking window has to start and end on the same
// day" — is gone. Somebody awake from noon until four keeps the hours they actually keep.
func TestAWakingWindowMaySpanMidnight(t *testing.T) {
	h := newHarness(t)
	p := h.signIn()

	resp := h.do("PATCH", "/api/rhythm", map[string]any{
		"wake_minute": 12 * 60, "sleep_minute": 4 * 60,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH = %s, want the hours accepted", resp.Status)
	}

	rh, err := h.store.Rhythm(h.Context(), p.ID)
	if err != nil {
		t.Fatalf("Rhythm(): %v", err)
	}
	if rh.WakeMinute != 12*60 || rh.SleepMinute != 4*60 {
		t.Errorf("hours = %d..%d, want them kept", rh.WakeMinute, rh.SleepMinute)
	}
	// Sixteen waking hours, not minus eight, which is what decides how far apart nudges land.
	if rh.Window() != 16*60 {
		t.Errorf("Window() = %d, want 960", rh.Window())
	}
}

// An hour outside a day is still a mistake, which is the bound the old CHECK was really for.
func TestAnHourOutsideADayIsStillRefused(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	for _, body := range []map[string]any{
		{"wake_minute": -1},
		{"sleep_minute": 25 * 60},
	} {
		resp := h.do("PATCH", "/api/rhythm", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PATCH %v = %s, want 400", body, resp.Status)
		}
	}
}
