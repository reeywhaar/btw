package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A real proxio, a real SOCKS5 server and a real target over loopback sockets, on the same
// argument internal/mail makes about starting an SMTP server: a mock would assert that net/http
// was called, and what is worth asserting is the conversation.

// proxio stands in for the relay: it takes the target as `url`, its credential as `token`, and
// hands back whatever the target said.
type proxio struct {
	token string

	sawToken  string
	sawURL    string
	sawMethod string
	sawBody   string
	sawAuth   string
}

func (p *proxio) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.sawToken = r.URL.Query().Get("token")
		p.sawURL = r.URL.Query().Get("url")
		p.sawMethod = r.Method
		p.sawAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		p.sawBody = string(body)

		if p.token != "" && p.sawToken != p.token {
			w.Header().Set("X-Proxio-Error", "bad token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Fetch the target the way proxio does, inheriting method and body.
		out, err := http.NewRequest(p.sawMethod, p.sawURL, strings.NewReader(p.sawBody))
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		out.Header = r.Header.Clone()
		res, err := http.DefaultClient.Do(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// target is what a request is actually trying to reach.
func target(t *testing.T) (addr string, seen *http.Request, body *string) {
	t.Helper()
	var got *http.Request
	var read string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		b, _ := io.ReadAll(r.Body)
		read = string(b)
		io.WriteString(w, "the gateway answered")
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got, &read
}

func post(t *testing.T, to, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, to, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest(): %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-or-v1-abc")
	req.Header.Set("Content-Type", "application/json")
	return req
}

// The whole reason proxio is usable here: the gateway is reached with a POST carrying JSON and
// an Authorization header, and a relay that only forwarded GETs would be no use at all.
func TestProxioCarriesTheMethodTheBodyAndTheHeaders(t *testing.T) {
	where, _, body := target(t)
	relay := &proxio{token: "px_secret"}
	at := relay.start(t)

	res, err := Send(t.Context(), post(t, where+"/api/v1/chat/completions", `{"model":"m"}`),
		Settings{Kind: Proxio, URL: at, Token: "px_secret", Enabled: true})
	if err != nil {
		t.Fatalf("Send(): %v", err)
	}
	defer res.Body.Close()

	if relay.sawMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", relay.sawMethod)
	}
	if relay.sawBody != `{"model":"m"}` {
		t.Errorf("body = %q, want it carried through", relay.sawBody)
	}
	if relay.sawAuth != "Bearer sk-or-v1-abc" {
		t.Errorf("Authorization = %q, want it carried through", relay.sawAuth)
	}
	if relay.sawToken != "px_secret" {
		t.Errorf("token = %q, want the credential sent as its own parameter", relay.sawToken)
	}
	if *body != `{"model":"m"}` {
		t.Errorf("the target read %q, want the body to have reached it", *body)
	}
	if got, _ := io.ReadAll(res.Body); string(got) != "the gateway answered" {
		t.Errorf("body = %q, want the target's own answer", got)
	}
}

// A caller reading res.Request.URL to say where it ended up would otherwise print
// `…/proxy?url=…&token=…` — the credential, into whatever it was writing.
func TestAResponseNeverPointsAtTheProxy(t *testing.T) {
	where, _, _ := target(t)
	at := (&proxio{}).start(t)

	res, err := Send(t.Context(), post(t, where, "{}"),
		Settings{Kind: Proxio, URL: at, Token: "px_secret", Enabled: true})
	if err != nil {
		t.Fatalf("Send(): %v", err)
	}
	defer res.Body.Close()

	got := res.Request.URL.String()
	if strings.Contains(got, "token=") || strings.Contains(got, "px_secret") {
		t.Fatalf("res.Request.URL = %q, which carries the credential", got)
	}
	if got != where {
		t.Errorf("res.Request.URL = %q, want the address that was asked for", got)
	}
}

// net/http wraps every transport failure in a *url.Error that prints what it dialled. Logged as
// it comes, one unreachable proxy writes the token into the log on every attempt.
func TestAFailureNeverCarriesTheCredential(t *testing.T) {
	// A port nothing is listening on, so the dial fails and the error is the transport's own.
	_, err := Send(t.Context(), post(t, "https://openrouter.ai/x", "{}"),
		Settings{Kind: Proxio, URL: "http://127.0.0.1:1", Token: "px_secret", Enabled: true})
	if err == nil {
		t.Fatal("Send() = nil, want the dial to have failed")
	}
	if strings.Contains(err.Error(), "px_secret") || strings.Contains(err.Error(), "token=") {
		t.Errorf("error = %q, which carries the credential", err)
	}

	// And the cause survives being scrubbed, or the message says nothing at all.
	if !strings.Contains(err.Error(), "proxy") {
		t.Errorf("error = %q, want it to name what failed", err)
	}
}

func TestScrubKeepsTheCauseAndDropsTheAddress(t *testing.T) {
	inner := errors.New("connection refused")
	wrapped := &url.Error{
		Op:  "Post",
		URL: "https://proxio.example.com/proxy?url=https%3A%2F%2Fopenrouter.ai&token=px_secret",
		Err: inner,
	}
	got := scrub(wrapped)
	if strings.Contains(got.Error(), "px_secret") {
		t.Errorf("scrub() = %q, which still carries the credential", got)
	}
	if !errors.Is(got, inner) {
		t.Errorf("scrub() = %v, want the cause kept", got)
	}
}

// Nothing goes anywhere but straight out unless a proxy is both configured and switched on.
// Off is the state somebody puts one in to find out whether it was the problem.
func TestNothingIsRoutedUntilItIsSwitchedOn(t *testing.T) {
	where, _, _ := target(t)
	relay := &proxio{}
	at := relay.start(t)

	for _, tc := range []struct {
		name string
		set  Settings
	}{
		{"nothing configured", Settings{}},
		{"switched off", Settings{Kind: Proxio, URL: at, Token: "t", Enabled: false}},
		{"no address", Settings{Kind: Proxio, Token: "t", Enabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Send(t.Context(), post(t, where, "{}"), tc.set)
			if err != nil {
				t.Fatalf("Send(): %v", err)
			}
			res.Body.Close()
			if relay.sawURL != "" {
				t.Errorf("the proxy was used with %+v", tc.set)
			}
		})
	}
}

// socksServer is a SOCKS5 endpoint that speaks just enough of RFC 1928 to be dialled through.
type socksServer struct {
	user, pass string
	dialled    int
}

func (s *socksServer) start(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	return "socks5://" + ln.Addr().String()
}

func (s *socksServer) handle(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 512)

	// Greeting: version, count, methods.
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	n := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:n]); err != nil {
		return
	}
	if s.user != "" {
		conn.Write([]byte{5, 2}) // username/password
		// Version, ulen, user, plen, pass.
		if _, err := io.ReadFull(conn, buf[:2]); err != nil {
			return
		}
		ulen := int(buf[1])
		if _, err := io.ReadFull(conn, buf[:ulen]); err != nil {
			return
		}
		gotUser := string(buf[:ulen])
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		plen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:plen]); err != nil {
			return
		}
		if gotUser != s.user || string(buf[:plen]) != s.pass {
			conn.Write([]byte{1, 1})
			return
		}
		conn.Write([]byte{1, 0})
	} else {
		conn.Write([]byte{5, 0}) // no authentication
	}

	// Request: version, command, reserved, address type.
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	var host string
	switch buf[3] {
	case 1: // IPv4
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 3: // a name, which is what socks5h semantics send
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:l]); err != nil {
			return
		}
		host = string(buf[:l])
	default:
		return
	}
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])

	upstream, err := net.Dial("tcp", net.JoinHostPort(host, itoa(port)))
	if err != nil {
		conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	s.dialled++
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	go io.Copy(upstream, conn)
	io.Copy(conn, upstream)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// SOCKS5 works at a different layer: the request is untouched and the connection is made
// somewhere else, so a response coming back already points at the target.
func TestSocksDialsThroughAndLeavesTheRequestAlone(t *testing.T) {
	where, _, body := target(t)
	socks := &socksServer{user: "misha", pass: "hunter2"}
	at := socks.start(t)

	res, err := Send(t.Context(), post(t, where+"/api/v1/chat/completions", `{"model":"m"}`),
		Settings{Kind: Socks, URL: at, Username: "misha", Token: "hunter2", Enabled: true})
	if err != nil {
		t.Fatalf("Send(): %v", err)
	}
	defer res.Body.Close()

	if socks.dialled == 0 {
		t.Error("the SOCKS endpoint was never dialled through")
	}
	if *body != `{"model":"m"}` {
		t.Errorf("the target read %q, want the body carried through", *body)
	}
	// Untouched means untouched: no path rewriting, no credential in the address.
	if got := res.Request.URL.String(); !strings.HasSuffix(got, "/api/v1/chat/completions") {
		t.Errorf("res.Request.URL = %q, want the address that was asked for", got)
	}
	if got, _ := io.ReadAll(res.Body); string(got) != "the gateway answered" {
		t.Errorf("body = %q, want the target's own answer", got)
	}
}

func TestAWrongSocksPasswordIsRefused(t *testing.T) {
	where, _, _ := target(t)
	at := (&socksServer{user: "misha", pass: "hunter2"}).start(t)

	_, err := Send(t.Context(), post(t, where, "{}"),
		Settings{Kind: Socks, URL: at, Username: "misha", Token: "wrong", Enabled: true})
	if err == nil {
		t.Fatal("Send() = nil, want the endpoint to have refused")
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error = %q, which carries the credential", err)
	}
}

// A client cached by anything identifying the row would go on using the old credential after a
// password was corrected — wrong exactly when it mattered.
func TestCorrectingAPasswordDoesNotReuseTheOldClient(t *testing.T) {
	a := fingerprint(Settings{Kind: Socks, URL: "socks5://h:1", Username: "u", Token: "old"})
	b := fingerprint(Settings{Kind: Socks, URL: "socks5://h:1", Username: "u", Token: "new"})
	if a == b {
		t.Error("two different passwords share a cache key")
	}
	// And the key does not carry the password into somewhere a panic would print it.
	if strings.Contains(a, "old") {
		t.Errorf("fingerprint = %q, which carries the credential", a)
	}
}
