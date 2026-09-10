// Package proxy sends a request from somewhere else — the one thing btw reaches on the open
// internet is an account's companion gateway, and some networks cannot.
//
// Somewhere else is the whole requirement and is easy to get wrong: a proxy container on the
// same compose network goes out from the same address, tests fine against anything
// unrestricted, and never once helps.
//
// One proxy, switched on or off. No ladder: a proxy that does not work is a thing to fix.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Kind is how a proxy is spoken to. The two are not variations on each other; what they share
// is being one address with one credential, which is why one row holds both.
type Kind string

const (
	// Proxio rewrites the request's URL: `GET /proxy?url=…&token=…`, over an ordinary
	// connection. Method, headers and body are inherited, which is what makes it usable here —
	// the gateway wants a POST with an Authorization header.
	Proxio Kind = "proxio"

	// Socks dials through a SOCKS5 endpoint: the request is untouched and the connection is
	// made somewhere else, so it needs its own dialer and a username beside the password.
	Socks Kind = "socks5"
)

func (k Kind) Valid() bool { return k == Proxio || k == Socks }

func (k Kind) NeedsUsername() bool { return k == Socks }

func (k Kind) Schemes() []string {
	if k == Socks {
		return []string{"socks5", "socks5h"}
	}
	return []string{"http", "https"}
}

// Example is a placeholder, and the address a refusal quotes.
func (k Kind) Example() string {
	if k == Socks {
		return "socks5://socks.example.com:1080"
	}
	return "https://proxio.example.com"
}

// Settings are the proxy as an operator configured it. Carries the credential.
type Settings struct {
	Kind Kind

	// URL is the proxy's own address, with no path.
	URL string

	// Username is empty for proxio, which authenticates with a secret alone.
	Username string

	// Token is proxio's token or SOCKS5's password. Never part of URL, which is what an
	// administrator's browser is shown — `socks5://user:pass@host` would put it on screen.
	Token string

	// Enabled is whether requests go through it. Off keeps the address and the credential, so
	// switching back on is a press rather than typing a secret again.
	Enabled bool
}

func (s Settings) Configured() bool { return s.Kind.Valid() && s.URL != "" }

func (s Settings) Active() bool { return s.Configured() && s.Enabled }

// connectTimeout bounds reaching the SOCKS endpoint, not the target beyond it: one that accepts
// a connection and says nothing must not spend the whole request budget.
const connectTimeout = 10 * time.Second

// Send sends a request, through the proxy when one is switched on and directly otherwise. The
// caller's context governs the deadline; how long a model may think is not this package's
// business.
func Send(ctx context.Context, req *http.Request, s Settings) (*http.Response, error) {
	if !s.Active() {
		res, err := http.DefaultClient.Do(req.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	send, client, err := dial(req, s)
	if err != nil {
		return nil, err
	}

	// A fresh body for every attempt: a request read once has nothing left to send.
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		send.Body = body
	}

	res, err := client.Do(send.WithContext(ctx))
	if err != nil {
		// Never the transport's own error: it is a *url.Error naming the address it dialled,
		// which for proxio ends `?url=…&token=…`.
		return nil, fmt.Errorf("could not reach the proxy: %w", scrub(err))
	}
	if res != nil {
		// A caller reading res.Request.URL would otherwise print the credential. Already true
		// of a SOCKS5 response, so this is a no-op there rather than a special case.
		res.Request = req
	}
	return res, nil
}

// dial is the request to send and the client to send it with. The two kinds diverge here and
// nowhere else.
func dial(req *http.Request, s Settings) (*http.Request, *http.Client, error) {
	switch s.Kind {
	case Proxio:
		sent, err := through(req, s)
		return sent, http.DefaultClient, err
	case Socks:
		client, err := clients.get(s)
		return req, client, err
	default:
		return nil, nil, fmt.Errorf("%q is not a kind of proxy this knows how to dial", s.Kind)
	}
}

// through rewrites a request to go via proxio. The stored address is a host and nothing else,
// so the path and query live here. The target is percent-encoded, which lets one carrying its
// own query string survive being nested inside another.
func through(req *http.Request, s Settings) (*http.Request, error) {
	target, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("the proxy has an address that will not parse: %w", err)
	}
	target.Path = "/proxy"
	target.RawQuery = url.Values{
		"url":   {req.URL.String()},
		"token": {s.Token},
		// Without it proxio adds an X-Forwarded-For naming the machine it stands in for.
		"hide": {"1"},
	}.Encode()

	out := req.Clone(req.Context())
	out.URL = target
	// Cleared so net/http fills it from the new URL, rather than naming the gateway.
	out.Host = ""
	return out, nil
}

// clients keeps one HTTP client per SOCKS endpoint, since a transport is where the connection
// pool lives. Keyed by what the connection depends on and not by the row, so a corrected
// password gets a new client rather than reusing the old credential.
var clients = &cache{clients: map[string]*http.Client{}}

// maxClients bounds the cache, which holds one entry on any real instance. Cleared rather than
// evicted: the cost of being wrong is rebuilding one transport.
const maxClients = 8

type cache struct {
	mu      sync.Mutex
	clients map[string]*http.Client
}

func (c *cache) get(s Settings) (*http.Client, error) {
	key := fingerprint(s)

	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.clients[key]; ok {
		return client, nil
	}

	client, err := socksClient(s)
	if err != nil {
		return nil, err
	}
	if len(c.clients) >= maxClients {
		for _, old := range c.clients {
			old.CloseIdleConnections()
		}
		clear(c.clients)
	}
	c.clients[key] = client
	return client, nil
}

// fingerprint is everything that changes what a connection would be. Hashed because the
// password is in it, and a map key ends up in panic messages and heap dumps.
func fingerprint(s Settings) string {
	sum := sha256.Sum256([]byte(string(s.Kind) + "\x00" + s.URL + "\x00" + s.Username + "\x00" + s.Token))
	return hex.EncodeToString(sum[:])
}

func socksClient(s Settings) (*http.Client, error) {
	target, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("the proxy has an address that will not parse: %w", err)
	}

	var auth *proxy.Auth
	if s.Username != "" || s.Token != "" {
		auth = &proxy.Auth{User: s.Username, Password: s.Token}
	}

	base := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}
	dialer, err := proxy.SOCKS5("tcp", target.Host, auth, base)
	if err != nil {
		return nil, fmt.Errorf("the proxy could not be set up: %w", err)
	}

	// Both schemes get socks5h semantics: x/net sends the name and lets the endpoint resolve it
	// unless the host already parses as an address. That is the useful behaviour — a host
	// blocked at DNS is only reached when the endpoint does the lookup — and socks5h is
	// accepted as the name people expect for it, not because it selects it.
	contextual, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("the proxy produced a dialer with no context support")
	}

	return &http.Client{
		Transport: &http.Transport{
			DialContext:           contextual.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}, nil
}

// scrub keeps the cause of a transport failure and drops the address it names: net/http wraps
// every one in a *url.Error printing what it dialled, which for proxio ends `?url=…&token=…`.
func scrub(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	// Belt and braces: a vague message beats one carrying the token.
	if strings.Contains(err.Error(), "token=") {
		return errors.New("the proxy refused the request")
	}
	return err
}
