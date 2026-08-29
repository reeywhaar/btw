package mail

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// relay is a fake SMTP server: enough of the protocol to hold a real conversation, and a
// record of everything that was said to it.
//
// Written rather than mocked because the thing worth testing is the conversation — that
// STARTTLS is demanded, that credentials do not go out in the clear, that the message ends
// with a lone dot. A mock of net/smtp would test that net/smtp was called.
type relay struct {
	addr string

	auth     []string // AUTH mechanisms to advertise; empty means offer none
	noTLSExt bool     // pretend not to speak STARTTLS
	rejectAt string   // the verb to answer 550 to

	got  []string // every line the client sent
	data string   // the message body
}

func startRelay(t *testing.T, implicit bool, r *relay) {
	t.Helper()

	cert, pool := selfSigned(t)
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	// Every dial in this package's tests trusts the certificate the test just minted, and
	// nothing else.
	rootCAs = pool
	t.Cleanup(func() { rootCAs = nil })

	var ln net.Listener
	var err error
	if implicit {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	r.addr = ln.Addr().String()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r.serve(t, conn, tlsConfig, implicit)
	}()
}

func (r *relay) serve(t *testing.T, conn net.Conn, tlsConfig *tls.Config, secure bool) {
	write := func(s string) { conn.Write([]byte(s + "\r\n")) }
	buf := make([]byte, 4096)
	var pending string

	readLine := func() (string, bool) {
		for {
			if i := strings.Index(pending, "\r\n"); i >= 0 {
				line := pending[:i]
				pending = pending[i+2:]
				return line, true
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := conn.Read(buf)
			if n == 0 || err != nil {
				return "", false
			}
			pending += string(buf[:n])
		}
	}

	write("220 fake.example.com ESMTP")
	for {
		line, ok := readLine()
		if !ok {
			return
		}
		r.got = append(r.got, line)
		verb := strings.ToUpper(strings.Fields(line + " ")[0])

		if r.rejectAt != "" && verb == r.rejectAt {
			write("550 no")
			continue
		}

		switch verb {
		case "EHLO":
			write("250-fake.example.com")
			if !secure && !r.noTLSExt {
				write("250-STARTTLS")
			}
			if secure && len(r.auth) > 0 {
				write("250-AUTH " + strings.Join(r.auth, " "))
			}
			write("250 SIZE 10240000")
		case "STARTTLS":
			write("220 go ahead")
			tc := tls.Server(conn, tlsConfig)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, secure, pending = tc, true, ""
			write = func(s string) { conn.Write([]byte(s + "\r\n")) }
		case "AUTH":
			if strings.Contains(strings.ToUpper(line), "LOGIN") && len(strings.Fields(line)) == 2 {
				write("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				u, _ := readLine()
				r.got = append(r.got, "login-user:"+decode(u))
				write("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				p, _ := readLine()
				r.got = append(r.got, "login-pass:"+decode(p))
			}
			write("235 ok")
		case "MAIL", "RCPT", "RSET", "NOOP":
			write("250 ok")
		case "DATA":
			write("354 send it")
			var body strings.Builder
			for {
				l, ok := readLine()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				body.WriteString(l + "\n")
			}
			r.data = body.String()
			write("250 queued")
		case "QUIT":
			write("221 bye")
			return
		default:
			write("500 what")
		}
	}
}

func decode(s string) string {
	b, _ := base64.StdEncoding.DecodeString(s)
	return string(b)
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	parsed, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}, pool
}

func settings(t *testing.T, r *relay, mode TLS) Settings {
	t.Helper()
	host, port, _ := net.SplitHostPort(r.addr)
	n, _ := strconv.Atoi(port)
	return Settings{
		Host: host, Port: n, TLS: mode,
		Username: "postmaster", Password: "hunter2",
		FromAddress: "btw@example.com", SenderName: "btw",
	}
}

func TestSendOverImplicitTLS(t *testing.T) {
	r := &relay{auth: []string{"PLAIN"}}
	startRelay(t, true, r)

	err := Send(t.Context(), settings(t, r, Implicit), Message{
		To: "misha@example.com", Subject: "Your code", Body: "It is ABCD1234.",
	})
	if err != nil {
		t.Fatalf("Send(): %v", err)
	}
	if !strings.Contains(r.data, "Subject: Your code") {
		t.Errorf("subject missing from:\n%s", r.data)
	}
	if !strings.Contains(r.data, "It is ABCD1234.") {
		t.Errorf("body missing from:\n%s", r.data)
	}
	if !strings.Contains(r.data, "From: \"btw\" <btw@example.com>") {
		t.Errorf("sender name not applied:\n%s", r.data)
	}
}

func TestSendOverStartTLS(t *testing.T) {
	r := &relay{auth: []string{"PLAIN"}}
	startRelay(t, false, r)

	if err := Send(t.Context(), settings(t, r, StartTLS), Message{
		To: "misha@example.com", Subject: "Hello", Body: "Hello.",
	}); err != nil {
		t.Fatalf("Send(): %v", err)
	}
	if !contains(r.got, "STARTTLS") {
		t.Error("the connection was never upgraded")
	}
	// Everything after the upgrade is inside TLS, so the credentials cannot appear among
	// the lines the relay read in the clear.
	for _, line := range r.got {
		if strings.Contains(line, "hunter2") {
			t.Fatalf("the password crossed in the clear: %q", line)
		}
	}
}

func TestARelayWithoutStartTLSIsRefusedByName(t *testing.T) {
	r := &relay{noTLSExt: true, auth: []string{"PLAIN"}}
	startRelay(t, false, r)

	err := Send(t.Context(), settings(t, r, StartTLS), Message{To: "a@example.com", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("Send() went ahead without encryption")
	}
	// The refusal says what is usually wrong, because a relay offering no STARTTLS on 587
	// is normally one expecting implicit TLS on 465.
	if !strings.Contains(err.Error(), "465") {
		t.Errorf("the refusal does not suggest the fix: %v", err)
	}
}

func TestLoginAuthWhenPlainIsNotOffered(t *testing.T) {
	r := &relay{auth: []string{"LOGIN"}}
	startRelay(t, true, r)

	if err := Send(t.Context(), settings(t, r, Implicit), Message{
		To: "misha@example.com", Subject: "s", Body: "b",
	}); err != nil {
		t.Fatalf("Send(): %v", err)
	}
	// Enough relays speak nothing but LOGIN that refusing it would mean refusing to send.
	if !contains(r.got, "login-user:postmaster") || !contains(r.got, "login-pass:hunter2") {
		t.Errorf("LOGIN was not completed: %v", r.got)
	}
}

func TestTheRelaysOwnWordsComeBack(t *testing.T) {
	r := &relay{auth: []string{"PLAIN"}, rejectAt: "RCPT"}
	startRelay(t, true, r)

	err := Send(t.Context(), settings(t, r, Implicit), Message{
		To: "nobody@example.com", Subject: "s", Body: "b",
	})
	if err == nil {
		t.Fatal("Send() reported success on a refusal")
	}
	// Which of the three things went wrong is three different afternoons, so the message
	// names the recipient and carries the relay's reply rather than "sending failed".
	if !strings.Contains(err.Error(), "nobody@example.com") || !strings.Contains(err.Error(), "550") {
		t.Errorf("the refusal lost the relay's answer: %v", err)
	}
}

func TestAnUnreachableRelayFailsQuickly(t *testing.T) {
	s := Settings{Host: "127.0.0.1", Port: 1, TLS: Implicit, FromAddress: "btw@example.com"}
	if err := Send(t.Context(), s, Message{To: "a@example.com", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("Send() to a closed port succeeded")
	}
}

func TestComposeQuotesADisplayNameThatNeedsIt(t *testing.T) {
	s := Settings{FromAddress: "btw@example.com", SenderName: "btw, the reminder"}
	raw, err := compose(s, Message{To: "misha@example.com", Subject: "s", Body: "b"})
	if err != nil {
		t.Fatalf("compose(): %v", err)
	}
	// A comma in a display name unquoted parses as two addresses.
	if !strings.Contains(string(raw), `"btw, the reminder" <btw@example.com>`) {
		t.Errorf("display name not quoted:\n%s", raw)
	}
}

func TestComposeEncodesANonASCIISubject(t *testing.T) {
	raw, err := compose(Settings{FromAddress: "btw@example.com"}, Message{
		To: "misha@example.com", Subject: "Пора", Body: "b",
	})
	if err != nil {
		t.Fatalf("compose(): %v", err)
	}
	if strings.Contains(string(raw), "Subject: Пора") {
		t.Error("a non-ASCII subject went out raw")
	}
	if !strings.Contains(string(raw), "Subject: =?utf-8?q?") {
		t.Errorf("subject not encoded:\n%s", raw)
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}
