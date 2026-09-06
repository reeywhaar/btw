package openrouter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"btw/internal/proxy"
)

// serve points the package at a server started here, for the length of one test.
//
// A real conversation over a loopback socket rather than a mocked http.Client, on the same
// argument internal/mail makes: a mock would assert that net/http was called, and what is
// worth asserting is that the key travels as a bearer token, that a fault inside a 200 is
// still a failure, and that the gateway's own words survive to the caller.
// direct is no proxy at all, which is what every test here wants but the one about proxies:
// naming it says these are about the gateway rather than about the way out.
var direct = proxy.Settings{}

func serve(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(SetEndpoint(srv.URL))
}

func ok(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}

func TestACheckCarriesTheKeyAndTheModel(t *testing.T) {
	var gotAuth, gotModel string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		ok(w, `{"model":"minimax/minimax-m3:free","choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":12}}`)
	})

	res, err := Check(t.Context(), Settings{APIKey: "sk-or-v1-abc", Model: "minimax/minimax-m3:free"}, direct)
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if gotAuth != "Bearer sk-or-v1-abc" {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}
	if gotModel != "minimax/minimax-m3:free" {
		t.Errorf("model = %q, want the configured one", gotModel)
	}
	if res.Tokens != 12 {
		t.Errorf("Tokens = %d, want 12", res.Tokens)
	}
}

// What answered is not always what was asked for: OpenRouter falls back between providers,
// and somebody trusting the answers wants to know which.
func TestTheModelThatAnsweredIsReportedAndNotTheOneAskedFor(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		ok(w, `{"model":"minimax/minimax-m2.7:free","choices":[{"message":{"content":"ok"}}]}`)
	})

	res, err := Check(t.Context(), Settings{APIKey: "k", Model: "minimax/minimax-m3:free"}, direct)
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if res.Model != "minimax/minimax-m2.7:free" {
		t.Errorf("Model = %q, want what actually served it", res.Model)
	}
}

// OpenRouter answers 200 with the fault in the body when a provider dies partway through, so
// the status code alone is not the whole story. This is the case a resp.StatusCode check
// would report as a success.
func TestAFaultInsideATwoHundredIsStillAFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"top level", `{"error":{"code":502,"message":"the provider went away"}}`},
		{"inside a choice", `{"choices":[{"finish_reason":"error","error":{"code":502,"message":"the provider went away"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serve(t, func(w http.ResponseWriter, _ *http.Request) { ok(w, tc.body) })

			_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
			if err == nil {
				t.Fatal("Check() = nil, want the fault reported")
			}
			if !strings.Contains(err.Error(), "the provider went away") {
				t.Errorf("error = %q, want the gateway's own words", err)
			}
		})
	}
}

// Four different afternoons. A single "that did not work" sends somebody to check the wrong
// thing first — a rejected key and a model with no credit are not the same mistake.
func TestEachRefusalSaysWhichKindItWas(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusUnauthorized, `{"error":{"code":401,"message":"No auth credentials found"}}`, "the key was rejected"},
		{http.StatusPaymentRequired, `{"error":{"code":402,"message":"Insufficient credits"}}`, "no credit"},
		{http.StatusNotFound, `{"error":{"code":404,"message":"No endpoints found for minimax/typo"}}`, "no such model"},
		{http.StatusTooManyRequests, `{"error":{"code":429,"message":"Rate limit exceeded"}}`, "rate limited"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})

			_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
			if err == nil {
				t.Fatalf("Check() = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to say %q", err, tc.want)
			}
		})
	}
}

// Usually a proxy's error page, and the page says which. Summarising it as "bad gateway"
// throws away the only sentence that identifies the proxy.
func TestABodyThatIsNotJSONIsQuotedRatherThanSummarised(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html><body>upstream connect error</body></html>")
	})

	_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
	if err == nil {
		t.Fatal("Check() = nil, want the page reported")
	}
	if !strings.Contains(err.Error(), "upstream connect error") {
		t.Errorf("error = %q, want the page's own words", err)
	}
}

func TestAGatewayThatAnsweredNothingIsAFailure(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		ok(w, `{"model":"m","choices":[]}`)
	})

	if _, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct); err == nil {
		t.Error("Check() = nil, want an empty reply refused")
	}
}

func TestThereIsNothingToCheckWithoutAKey(t *testing.T) {
	// No server: reaching one at all would be the bug.
	if _, err := Check(t.Context(), Settings{Model: "m"}, direct); err == nil {
		t.Error("Check() = nil, want a refusal before any request")
	}
}

// A quota is the one refusal here that will pass on its own, and a caller's response to it is
// categorically different: it wants a wait, not a person. Asserted through the sentinel rather
// than the wording, because the wording is the gateway's and will change.
func TestARateLimitIsTellableApartFromEveryOtherRefusal(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"code":429,"message":"Rate limit exceeded, free-models-per-day"}}`)
	})

	_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("Check() = %v, want it to satisfy errors.Is(ErrRateLimited)", err)
	}
	// And the gateway's own words survive alongside the kind, since "per day" and "per minute"
	// are hours apart.
	if !strings.Contains(err.Error(), "free-models-per-day") {
		t.Errorf("error = %q, want the gateway's own words kept", err)
	}

	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":401,"message":"No auth credentials found"}}`)
	})
	if _, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct); errors.Is(err, ErrRateLimited) {
		t.Error("a rejected key was reported as a rate limit")
	}
}

// An account that never chose a model follows the default wherever it goes, and the resolving
// happens here rather than in the row — so the question asked names a model even when nothing
// was written down.
func TestAnUnchosenModelIsResolvedWhenItIsAsked(t *testing.T) {
	var asked string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		asked = body.Model
		ok(w, `{"model":"m","choices":[{"message":{"content":"ok"}}]}`)
	})

	if _, err := Check(t.Context(), Settings{APIKey: "k"}, direct); err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if asked != DefaultModel {
		t.Errorf("asked %q, want %q — an empty model must not reach the gateway", asked, DefaultModel)
	}

	// And one that was chosen is the one asked for.
	if _, err := Check(t.Context(), Settings{APIKey: "k", Model: "minimax/minimax-m3"}, direct); err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if asked != "minimax/minimax-m3" {
		t.Errorf("asked %q, want the chosen one", asked)
	}
}
