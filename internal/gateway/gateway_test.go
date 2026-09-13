package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"btw/internal/proxy"
)

// direct is no proxy at all, which is what every test here wants but the one about proxies.
var direct = proxy.Settings{}

// serve points the package at a server started here, for the length of one test. A real
// conversation over a loopback socket rather than a mocked http.Client, on the argument
// internal/mail makes: a mock would assert that net/http was called.
func serve(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(SetEndpoint(srv.URL))
}

// check drives one exchange. Check is gone — a one-word probe proved a key and a slug and
// nothing about the thing the loop does — so these go through Ask, which is what runs.
func check(ctx context.Context, set Settings, via proxy.Settings) (Result, error) {
	_, res, err := Ask(ctx, set, via, "a system prompt", "a question", 200)
	return res, err
}

func ok(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}

func TestAnExchangeCarriesTheKeyAndTheModel(t *testing.T) {
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

	res, err := check(t.Context(), Settings{APIKey: "sk-or-v1-abc", Model: "minimax/minimax-m3:free"}, direct)
	if err != nil {
		t.Fatalf("Ask(): %v", err)
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

	res, err := check(t.Context(), Settings{APIKey: "k", Model: "minimax/minimax-m3:free"}, direct)
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if res.Model != "minimax/minimax-m2.7:free" {
		t.Errorf("Model = %q, want what actually served it", res.Model)
	}
}

// OpenRouter answers 200 with the fault in the body when a provider dies partway through, which
// a resp.StatusCode check reports as a success.
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

			_, err := check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
			if err == nil {
				t.Fatal("Ask() = nil, want the fault reported")
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

			_, err := check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
			if err == nil {
				t.Fatalf("Ask() = nil, want a refusal")
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

	_, err := check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
	if err == nil {
		t.Fatal("Ask() = nil, want the page reported")
	}
	if !strings.Contains(err.Error(), "upstream connect error") {
		t.Errorf("error = %q, want the page's own words", err)
	}
}

func TestAGatewayThatAnsweredNothingIsAFailure(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		ok(w, `{"model":"m","choices":[]}`)
	})

	if _, err := check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct); err == nil {
		t.Error("Ask() = nil, want an empty reply refused")
	}
}

func TestThereIsNothingToCheckWithoutAKey(t *testing.T) {
	// No server: reaching one at all would be the bug.
	if _, err := check(t.Context(), Settings{Model: "m"}, direct); err == nil {
		t.Error("Ask() = nil, want a refusal before any request")
	}
}

// A quota wants a wait, every other refusal wants a person. Asserted through the sentinel, since
// the wording is the gateway's and will change.
func TestARateLimitIsTellableApartFromEveryOtherRefusal(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"code":429,"message":"Rate limit exceeded, free-models-per-day"}}`)
	})

	_, err := check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct)
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("Ask() = %v, want it to satisfy errors.Is(ErrRateLimited)", err)
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
	if _, err := check(t.Context(), Settings{APIKey: "k", Model: "m"}, direct); errors.Is(err, ErrRateLimited) {
		t.Error("a rejected key was reported as a rate limit")
	}
}

// Resolved here rather than in the row, so an account that never chose follows the default
// wherever it goes.
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

	if _, err := check(t.Context(), Settings{APIKey: "k"}, direct); err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if asked != DefaultModel {
		t.Errorf("asked %q, want %q — an empty model must not reach the gateway", asked, DefaultModel)
	}

	// And one that was chosen is the one asked for.
	if _, err := check(t.Context(), Settings{APIKey: "k", Model: "minimax/minimax-m3"}, direct); err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if asked != "minimax/minimax-m3" {
		t.Errorf("asked %q, want the chosen one", asked)
	}
}

// reasoning is OpenRouter's own field. The Hugging Face router hands the body to whichever
// provider serves the model, and one that rejects an unknown field is a 400 nobody can explain
// from the message.
func TestOnlyOpenRouterIsSentItsOwnFields(t *testing.T) {
	var body map[string]any
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		// A fresh map each time: decoding into one already holding keys merges rather than
		// replaces, and the second request would inherit the first's fields.
		body = map[string]any{}
		json.NewDecoder(r.Body).Decode(&body)
		ok(w, `{"model":"m","choices":[{"message":{"content":"{}"}}]}`)
	})

	for _, tc := range []struct {
		provider Provider
		want     bool
	}{
		{OpenRouter, true},
		{HuggingFace, false},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			if _, _, err := Ask(t.Context(), Settings{Provider: tc.provider, APIKey: "k"},
				direct, "s", "u", 200); err != nil {
				t.Fatalf("Ask(): %v", err)
			}
			if _, sent := body["reasoning"]; sent != tc.want {
				t.Errorf("reasoning sent = %v, want %v", sent, tc.want)
			}
			// Never to Hugging Face: a model whose thinking mode is required refuses the
			// whole request over this field rather than ignoring it.
			if _, sent := body["chat_template_kwargs"]; sent {
				t.Error("chat_template_kwargs was sent, which a required-thinking model refuses")
			}
			// What both accept, and what the answer being readable depends on.
			if body["response_format"] == nil {
				t.Error("response_format was not sent")
			}
			if body["model"] != tc.provider.DefaultModel() {
				t.Errorf("model = %v, want this service's own default", body["model"])
			}
		})
	}
}

// Two addresses, and a key for one is not a key for the other — so the provider has to reach
// the request rather than being decoration on a form.
func TestEachServiceHasItsOwnAddressAndDefault(t *testing.T) {
	if OpenRouter.Endpoint() == HuggingFace.Endpoint() {
		t.Error("both services share an address")
	}
	if OpenRouter.DefaultModel() == HuggingFace.DefaultModel() {
		t.Error("both services share a default model")
	}
	// A row written before there were two still asks somewhere.
	if (Provider("")).OrDefault() != OpenRouter || Provider("anthropic").OrDefault() != OpenRouter {
		t.Error("an unset or unknown service does not fall back to the default")
	}
}

// A caller's shorter deadline has to survive the one applied here, or every caller's is
// decoration and only this package's number means anything.
func TestACallersOwnDeadlineIsNotOverridden(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, _, err := Ask(ctx, Settings{APIKey: "k"}, direct, "s", "u", 200)
	if err == nil {
		t.Fatal("Ask() = nil, want the caller's deadline to end it")
	}
	// Comfortably under Timeout, which is what proves whose deadline was used.
	if took := time.Since(started); took > 30*time.Second {
		t.Errorf("took %s, want the caller's 100ms rather than the package's %s", took, Timeout)
	}
}
