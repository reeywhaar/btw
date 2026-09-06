// Package proxy sends a request from somewhere else.
//
// btw reaches exactly one thing on the open internet: the gateway an account's
// [companion](../../docs/companion.md) has a key for. Some networks cannot reach it — a
// provider that blocks the host, a machine behind a filter, a region the gateway refuses —
// and the shape is always the same: the gateway is fine and *we* are the problem.
//
// **Somewhere else is the whole requirement, and it is easy to get wrong.** A proxy container
// on the same compose network as btw goes out from the same address, so a filter that blocks
// this instance blocks the proxy identically. It tests fine against anything unrestricted and
// never once helps. A proxy has to be on a network that can reach what this one cannot.
//
// There is no ladder and no fallback here, unlike the elaborate version this is descended
// from. One proxy, switched on or off. btw makes a handful of requests an hour to a single
// host, so trying several in turn would be machinery guarding a cost that does not exist —
// and a proxy that is configured and does not work is a thing to fix rather than to route
// around silently.
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

// Kind is how a proxy is spoken to.
//
// Two, and they are not variations on each other — see the constants. What they share is being
// one address with one credential that a request can be sent through, which is why one row
// holds both.
type Kind string

const (
	// Proxio rewrites the request's URL: `GET /proxy?url=…&token=…`, which fetches the address
	// and hands back what it got. The connection is an ordinary one, so it needs nothing but a
	// URL and a token.
	//
	// The method, the headers and the body are inherited, which is what makes this usable at
	// all here: the gateway is reached with a POST carrying JSON and an Authorization header,
	// and a relay that only forwarded GETs would be useless for it.
	Proxio Kind = "proxio"

	// Socks dials through a SOCKS5 endpoint.
	//
	// A different layer: the request is untouched and the *connection* is made somewhere else,
	// so it needs its own dialer rather than its own URL. Authenticates with a username beside
	// the password, where proxio takes one opaque token.
	Socks Kind = "socks5"
)

// Valid reports whether k is a kind this program knows how to dial.
func (k Kind) Valid() bool { return k == Proxio || k == Socks }

// NeedsUsername reports whether this kind authenticates with a name as well as a secret.
func (k Kind) NeedsUsername() bool { return k == Socks }

// Schemes is what an address of this kind must be written as.
func (k Kind) Schemes() []string {
	if k == Socks {
		return []string{"socks5", "socks5h"}
	}
	return []string{"http", "https"}
}

// Example is an address of this kind, for a placeholder and for a refusal to agree on.
func (k Kind) Example() string {
	if k == Socks {
		return "socks5://socks.example.com:1080"
	}
	return "https://proxio.example.com"
}

// Settings are the proxy as an operator configured it. Carries the credential.
type Settings struct {
	Kind Kind

	// URL is the proxy's own address with no path. How a request is built out of it is the
	// kind's business.
	URL string

	// Username is empty for kinds that authenticate with a secret alone, which is proxio.
	Username string

	// Token is the secret: proxio's token, or SOCKS5's password.
	//
	// Never part of URL, which is what an administrator's browser is shown.
	// `socks5://user:pass@host` is the natural way to write one of these down, and it would
	// put the password on screen.
	Token string

	// Enabled is whether requests actually go through it.
	//
	// Off keeps the address and the credential, so switching back on is a press rather than
	// typing a secret again — which is the difference between turning something off to find
	// out whether it was the problem and deleting it to find out.
	Enabled bool
}

// Configured reports whether a proxy has been set up at all.
func (s Settings) Configured() bool { return s.Kind.Valid() && s.URL != "" }

// Active reports whether requests should go through it right now.
func (s Settings) Active() bool { return s.Configured() && s.Enabled }

// connectTimeout bounds reaching a SOCKS endpoint itself, as opposed to the target beyond it.
//
// Separate from whatever deadline the caller's context carries: an endpoint that accepts a
// connection and then says nothing must not spend the whole request budget before the gateway
// is reached.
const connectTimeout = 10 * time.Second

// Send sends a request, through the proxy when one is switched on and directly otherwise.
//
// The context governs the overall deadline; the caller sets it, because how long a model may
// think is the caller's business and not this package's.
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

	// A fresh body for every attempt. A request read once is a request with nothing left to
	// send, and rebuilding is cheap next to discovering the day a retry is added that the
	// second attempt posted nothing.
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		send.Body = body
	}

	res, err := client.Do(send.WithContext(ctx))
	if err != nil {
		// Never the transport's own error, which is a *url.Error carrying the whole address it
		// dialled — and for proxio that address ends `?url=…&token=…`. Logged as it comes,
		// every failed attempt writes the credential into the log file.
		return nil, fmt.Errorf("could not reach the proxy: %w", scrub(err))
	}
	if res != nil {
		// Point the response back at what was asked for rather than at the proxy.
		//
		// A caller reading res.Request.URL to say where it ended up would otherwise print
		// `…/proxy?url=…&token=…` — the credential, into whatever it was writing. A SOCKS5
		// response already points at the target, so this is a no-op for that kind rather than
		// a special case.
		res.Request = req
	}
	return res, nil
}

// dial is the request to send and the client to send it with.
//
// The two kinds diverge here and nowhere else. proxio is a rewritten URL over the ordinary
// client; SOCKS5 is the same URL over a client that dials somewhere else first.
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

// through rewrites a request to go via proxio.
//
// The stored address is a host and nothing else, so the path and the shape of the query live
// here, where the kind is known. The target goes in percent-encoded, which is what lets one
// carrying its own query string survive being nested inside another.
//
// The method, headers and body are the caller's: proxio inherits them, so the request that
// goes out is the request that would have gone out.
func through(req *http.Request, s Settings) (*http.Request, error) {
	target, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("the proxy has an address that will not parse: %w", err)
	}
	target.Path = "/proxy"
	target.RawQuery = url.Values{
		"url":   {req.URL.String()},
		"token": {s.Token},
		// This instance's own address is nobody's business but ours: without it, proxio adds
		// an X-Forwarded-For naming the machine the proxy was there to stand in for.
		"hide": {"1"},
	}.Encode()

	out := req.Clone(req.Context())
	out.URL = target
	// Cleared so net/http fills it from the new URL. Left as the gateway's host, the proxy
	// would be sent a Host header naming somewhere it is not.
	out.Host = ""
	return out, nil
}

// clients keeps one HTTP client per SOCKS endpoint, so a connection can be reused.
//
// A transport is meant to be long-lived and shared — it is where the connection pool lives —
// and building one per request means a fresh TCP handshake, a fresh SOCKS handshake and a
// fresh TLS handshake every time.
//
// Keyed by what the connection actually depends on rather than by anything identifying the
// row, so a proxy whose password is corrected gets a new client rather than going on using the
// old credential out of a cache. A row id would be the tempting key and would be wrong exactly
// when it mattered.
var clients = &cache{clients: map[string]*http.Client{}}

// maxClients bounds the cache. It only grows when the address or the credential changes, so on
// any real instance it holds one entry; the bound is for something rewriting the settings in a
// loop. Clearing rather than evicting keeps it to a few lines, and the cost of being wrong is
// rebuilding one transport.
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

// fingerprint is everything about a proxy that changes what a connection through it would be.
//
// Hashed rather than concatenated because the password is in it, and a map key is the sort of
// thing that ends up in a panic message or a heap dump.
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

	// socks5h and socks5 behave identically here, and that is worth writing down rather than
	// leaving as a coincidence.
	//
	// The two schemes differ over who resolves the name: socks5 looks it up locally and sends
	// an address, socks5h sends the name and lets the endpoint look it up. x/net does the
	// second unconditionally — it sends an address only when the host already parses as one —
	// so both schemes get socks5h semantics. Which is the useful one anyway: a host blocked at
	// DNS rather than at the address is only reached when the endpoint does the lookup.
	// socks5h is accepted as the name people expect for that behaviour, not because it selects
	// it.
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

// scrub keeps the cause of a transport failure and drops the address it names.
//
// net/http wraps every one in a *url.Error that prints what it dialled, and for proxio that
// ends `?url=…&token=…`. One unreachable proxy would write the credential into the log on
// every attempt.
func scrub(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	// Belt and braces for anything that formatted the address into a plain error before this
	// saw it: a message carrying the token is worse than a vague one.
	if strings.Contains(err.Error(), "token=") {
		return errors.New("the proxy refused the request")
	}
	return err
}
