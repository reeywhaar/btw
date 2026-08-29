// Package mail speaks SMTP to whichever relay an operator has configured.
//
// btw does not deliver mail. It hands a message to a relay somebody already has — their
// provider, or a sending service — and that relay does the rest.
//
// Split from the store on purpose: the store decides what the relay *is* and holds its
// password, and this is the half that opens a socket. Nothing here touches the database.
//
// Nothing is queued either. A message goes out while the request that asked for it is still
// open, so the caller learns whether the relay accepted it. That is the whole point of a
// test send, and it is what a later caller wants too — a recovery code that failed silently
// is worse than one that failed loudly. If btw ever sends enough mail for that to hurt, a
// queue is a change to this file rather than to its callers.
package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strings"
	"time"
)

// TLS is how the connection is protected. There is no third option, on purpose: a password
// crossing the network in the clear is not a choice somebody should be able to make by
// accident.
type TLS string

const (
	// StartTLS upgrades a plain connection, which is what port 587 expects.
	StartTLS TLS = "starttls"
	// Implicit is TLS from the first byte, which is what port 465 expects.
	Implicit TLS = "implicit"
)

// Timeout caps the whole conversation — connect, greet, authenticate, send, quit.
//
// A relay that has stopped answering must not hold a request open until the browser gives
// up first, because then nobody learns why.
const Timeout = 30 * time.Second

// rootCAs is nil, which means the system trust store — the right answer everywhere except a
// test, which has to trust a relay it started itself.
var rootCAs *x509.CertPool

// Settings are the relay as an operator configured it. Carries the password.
type Settings struct {
	Host        string
	Port        int
	TLS         TLS
	Username    string
	Password    string
	FromAddress string
	SenderName  string
}

// Configured reports whether there is a relay to send through at all.
func (s Settings) Configured() bool { return s.Host != "" && s.FromAddress != "" }

// Message is one mail, before any of it is encoded.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Send delivers one message, and reports what the relay said if it refused.
//
// The relay's own words are passed back rather than replaced with "sending failed". An
// operator setting this up needs to know whether the host was wrong, the credentials were
// rejected, or the certificate did not verify, and those are three different afternoons.
func Send(ctx context.Context, s Settings, m Message) error {
	raw, err := compose(s, m)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	c, err := dial(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := authenticate(c, s); err != nil {
		return err
	}
	if err := c.Mail(s.FromAddress); err != nil {
		return fmt.Errorf("the relay refused the sender %s: %w", s.FromAddress, err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("the relay refused the recipient %s: %w", m.To, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("the relay refused the message: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("the relay refused the message: %w", err)
	}
	// Closing is what sends it: the final dot goes out here, and this is the error that says
	// whether it was accepted. Deferring the close would discard exactly that.
	if err := w.Close(); err != nil {
		return fmt.Errorf("the relay refused the message: %w", err)
	}
	return c.Quit()
}

// dial opens the connection and gets TLS around it, one way or the other.
//
// Built per send rather than pooled. This runs when somebody presses a button, not in a
// loop, and a connection held open against a relay that may rotate its certificate is a
// failure saved up for later.
func dial(ctx context.Context, s Settings) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	d := &net.Dialer{}
	tlsConfig := &tls.Config{ServerName: s.Host, RootCAs: rootCAs, MinVersion: tls.VersionTLS12}

	if s.TLS == Implicit {
		conn, err := (&tls.Dialer{NetDialer: d, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("could not open a TLS connection to %s: %w", addr, err)
		}
		c, err := smtp.NewClient(conn, s.Host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("%s did not greet us as an SMTP relay: %w", addr, err)
		}
		return c, nil
	}

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("could not connect to %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%s did not greet us as an SMTP relay: %w", addr, err)
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		c.Close()
		// Named rather than fallen back from, and the message says what is usually wrong:
		// a relay offering no STARTTLS on 587 is normally one expecting implicit TLS on 465.
		return nil, fmt.Errorf("%s does not offer STARTTLS, so the password would cross in the clear; try implicit TLS on port 465", addr)
	}
	if err := c.StartTLS(tlsConfig); err != nil {
		c.Close()
		return nil, fmt.Errorf("could not start TLS with %s: %w", addr, err)
	}
	return c, nil
}

// authenticate offers PLAIN, then LOGIN.
//
// LOGIN is the same credentials in a sillier shape, and enough relays speak nothing else
// that refusing it would mean refusing to send at all. Both are refused by their
// implementations over an unencrypted connection, which by here cannot happen anyway.
func authenticate(c *smtp.Client, s Settings) error {
	if s.Username == "" {
		// A relay that wants no credentials exists, but accepting a blank password would
		// make "this relay needs none" indistinguishable from "somebody left the field
		// empty", and the second is far likelier. The store refuses to save one.
		return nil
	}
	ok, mechanisms := c.Extension("AUTH")
	if !ok {
		return errors.New("the relay does not offer authentication, but a username was configured")
	}

	var auth smtp.Auth
	switch {
	case strings.Contains(mechanisms, "PLAIN"):
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	case strings.Contains(mechanisms, "LOGIN"):
		auth = loginAuth{username: s.Username, password: s.Password, host: s.Host}
	default:
		return fmt.Errorf("the relay offers only %s, and btw speaks PLAIN and LOGIN", mechanisms)
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("the relay rejected the credentials: %w", err)
	}
	return nil
}

// loginAuth is AUTH LOGIN, which net/smtp does not implement.
type loginAuth struct{ username, password, host string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// The same guard PlainAuth makes: credentials do not go over a connection that is not
	// encrypted, and not to a host other than the one that was configured.
	if !server.TLS {
		return "", nil, errors.New("refusing to send credentials over an unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, fmt.Errorf("refusing to send credentials to %s, which is not %s", server.Name, a.host)
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	// The prompts are "Username:" and "Password:" base64-encoded, but relays differ on
	// capitalisation and punctuation, so the order is what decides rather than the text.
	switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(string(fromServer), ":"))) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("the relay asked for %q during LOGIN, which btw does not understand", fromServer)
}

// compose builds the message.
//
// By hand rather than with a library: it is a dozen headers and a quoted-printable body.
// Addresses go through net/mail, because a display name needs quoting when it holds a comma
// and encoding when it holds anything outside ASCII, and either done by hand produces a
// header that parses as a different address than the one meant.
func compose(s Settings, m Message) ([]byte, error) {
	from, err := netmail.ParseAddress(s.FromAddress)
	if err != nil {
		return nil, fmt.Errorf("%q is not an address the relay could send from: %w", s.FromAddress, err)
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("%q is not an address: %w", m.To, err)
	}
	if s.SenderName != "" {
		from.Name = s.SenderName
	}

	var body strings.Builder
	qp := quotedprintable.NewWriter(&body)
	if _, err := qp.Write([]byte(m.Body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", to.String())
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(body.String())
	return []byte(b.String()), nil
}
