package openrouter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve points the package at a server started here, for the length of one test.
//
// A real conversation over a loopback socket rather than a mocked http.Client, on the same
// argument internal/mail makes: a mock would assert that net/http was called, and what is
// worth asserting is that the key travels as a bearer token, that a fault inside a 200 is
// still a failure, and that the gateway's own words survive to the caller.
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

	res, err := Check(t.Context(), Settings{APIKey: "sk-or-v1-abc", Model: "minimax/minimax-m3:free"})
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

	res, err := Check(t.Context(), Settings{APIKey: "k", Model: "minimax/minimax-m3:free"})
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

			_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"})
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
		{http.StatusTooManyRequests, `{"error":{"code":429,"message":"Rate limit exceeded"}}`, "too many requests"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})

			_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"})
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

	_, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"})
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

	if _, err := Check(t.Context(), Settings{APIKey: "k", Model: "m"}); err == nil {
		t.Error("Check() = nil, want an empty reply refused")
	}
}

func TestThereIsNothingToCheckWithoutAKey(t *testing.T) {
	// No server: reaching one at all would be the bug.
	if _, err := Check(t.Context(), Settings{Model: "m"}); err == nil {
		t.Error("Check() = nil, want a refusal before any request")
	}
}
