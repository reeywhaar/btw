// Package openrouter puts a question to a model, through the gateway an account has a key for.
//
// Split from the store on the same seam as [btw/internal/mail]: nothing here touches the
// database. OpenRouter rather than a vendor directly, so the difference between two models is a
// string and the choice can be a form field.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"btw/internal/proxy"
)

const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// DefaultModel is the fallback under an instance's own, which is what an account gets when
// neither has been set. Free, so this can be tried without a balance. The `:free` variants
// accept far fewer parameters than the paid slug of the same model — no `structured_outputs` —
// so anything wanting a strict schema has to check.
const DefaultModel = "minimax/minimax-m3:free"

// ErrRateLimited is the one refusal that passes on its own. A sentinel, because a loop that
// cannot tell it from the rest either hammers an exhausted quota or gives up on a good key.
var ErrRateLimited = errors.New("rate limited")

// Timeout caps one exchange. Long, because a reasoning model on a free endpoint queues; still a
// cap, so a dead gateway does not hold the request until the browser gives up first.
const Timeout = 60 * time.Second

// endpoint is where requests go. A var only so a test can point it at a server of its own.
var endpoint = Endpoint

// SetEndpoint replaces where requests go and returns a function putting the old one back. For
// tests; exported because the handler deciding which key to try is in another package.
func SetEndpoint(u string) func() {
	old := endpoint
	endpoint = u
	return func() { endpoint = old }
}

// Settings are the companion as somebody configured it. Carries the key.
type Settings struct {
	APIKey string

	// Model is what the account chose, or empty for whatever the default is now. Empty is a
	// state, not a gap: filling it in on the way to the database would pin an account to
	// today's default, so it is resolved at the moment of asking instead.
	Model string

	// Default is the instance's model, which an administrator sets and an unchosen Model
	// follows. Empty falls through to [DefaultModel].
	Default string

	// About is what the model is told about the person, in their own words — the whole reason a
	// companion can say anything useful. A rhythm's waking window says which hours are allowed,
	// never which are wanted.
	About string
}

func (s Settings) ModelOrDefault() string {
	for _, m := range []string{s.Model, s.Default, DefaultModel} {
		if m != "" {
			return m
		}
	}
	return DefaultModel
}

// Configured is the key alone: a key without an About still answers, worse than it would with
// one.
func (s Settings) Configured() bool { return s.APIKey != "" }

// Result is what a successful exchange says about itself.
type Result struct {
	// Model is what actually served the request, which is not always what was asked for:
	// OpenRouter falls back between providers and a slug can resolve to a variant.
	Model string

	// Tokens is what the exchange cost, so a test press says something about the next thousand.
	Tokens int

	// Truncated is whether the model ran out of ceiling mid-answer.
	Truncated bool

	// reply is unexported so [Check]'s caller cannot depend on the word the model happened to
	// say; [Ask]'s gets it through a return value that means it.
	reply string
}

// Check asks the model to say one word, and reports what came back.
//
// A real completion rather than `GET /api/v1/key`, which would prove the key is live and
// nothing about the model — the half somebody is likelier to get wrong. One completion proves
// both, and on the default model it costs nothing.
func Check(ctx context.Context, set Settings, via proxy.Settings) (Result, error) {
	if !set.Configured() {
		return Result{}, errors.New("there is no key to check")
	}

	body := map[string]any{
		"model": set.ModelOrDefault(),
		// Small, but a reasoning model's thinking counts against the same ceiling, so not so
		// small that the reply is cut off before it starts.
		"max_tokens": 200,
		"reasoning":  map[string]any{"exclude": true},
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with the single word: ok"},
		},
	}
	return post(ctx, set.APIKey, body, via)
}

// Ask puts a prompt to the configured model and returns what it said — the general form of
// [Check].
//
// JSON mode rather than a strict schema: a `:free` variant advertises `response_format` without
// `structured_outputs`, so asking for a schema it cannot honour gets prose back from a request
// that looked like it demanded otherwise.
func Ask(ctx context.Context, set Settings, via proxy.Settings, system, user string, maxTokens int) (string, Result, error) {
	if !set.Configured() {
		return "", Result{}, errors.New("there is no companion to ask")
	}

	body := map[string]any{
		"model":      set.ModelOrDefault(),
		"max_tokens": maxTokens,
		// A reasoning model otherwise leaks its thinking into the content, which is the
		// likeliest reason a JSON answer fails to parse.
		"reasoning": map[string]any{"exclude": true},
		// Low, not zero: this is extraction, not invention.
		"temperature": 0.2,
		// Fresh every time, deliberately. A fixed seed would put a reminder wrongly in the same
		// half of the week forever, however often the question was asked again.
		"seed":            rand.Int64(),
		"response_format": map[string]any{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}

	res, err := post(ctx, set.APIKey, body, via)
	if err != nil {
		return "", Result{}, err
	}
	if res.Truncated {
		// "unexpected end of input" would send somebody to the prompt when the fix is a bigger
		// ceiling.
		return "", res, errors.New("the answer was cut off before it finished; allow more tokens")
	}
	return res.reply, res, nil
}

type completion struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
		Error        *fault `json:"error"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Error *fault `json:"error"`
}

// fault is OpenRouter's error shape, which is the same whether it arrives with a matching
// status or inside a 200.
type fault struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func post(ctx context.Context, key string, body map[string]any, via proxy.Settings) (Result, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Result{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	// The decision is entirely proxy.Send's, which is what keeps the two paths from drifting.
	resp, err := proxy.Send(ctx, req, via)
	if err != nil {
		return Result{}, fmt.Errorf("reach the gateway: %w", err)
	}
	defer resp.Body.Close()

	// Bounded: a body that never ends would outlive the timeout meant to bound it.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read the reply: %w", err)
	}

	var parsed completion
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// Quoted rather than summarised: it is usually a proxy's error page, and it says which.
		return Result{}, fmt.Errorf("%s: %s", resp.Status, snippet(raw))
	}

	// OpenRouter answers 200 with the fault in the body when a provider dies partway through.
	if f := firstFault(parsed); f != nil {
		return Result{}, explain(f.Code, resp.StatusCode, f.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, explain(resp.StatusCode, resp.StatusCode, snippet(raw))
	}
	if len(parsed.Choices) == 0 {
		return Result{}, errors.New("the gateway answered with no reply at all")
	}

	return Result{
		Model:     parsed.Model,
		Tokens:    parsed.Usage.TotalTokens,
		Truncated: parsed.Choices[0].FinishReason == "length",
		reply:     strings.TrimSpace(parsed.Choices[0].Message.Content),
	}, nil
}

func firstFault(c completion) *fault {
	if c.Error != nil {
		return c.Error
	}
	for _, choice := range c.Choices {
		if choice.Error != nil {
			return choice.Error
		}
	}
	return nil
}

// explain turns a status into the sentence somebody can act on, with the gateway's own words
// beside it and never instead: a single "that did not work" sends them to the wrong field.
func explain(code, status int, message string) error {
	if code == 0 {
		code = status
	}
	said := strings.TrimSpace(message)
	if said == "" {
		said = http.StatusText(code)
	}
	switch code {
	case http.StatusUnauthorized:
		return fmt.Errorf("the key was rejected: %s", said)
	case http.StatusPaymentRequired:
		return fmt.Errorf("the key has no credit for that model: %s", said)
	case http.StatusNotFound:
		return fmt.Errorf("no such model: %s", said)
	case http.StatusTooManyRequests:
		// Not a mistake: free models allow twenty a minute and fifty a day.
		return fmt.Errorf("%w: %s", ErrRateLimited, said)
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return fmt.Errorf("the model is unavailable: %s", said)
	default:
		return fmt.Errorf("the gateway refused it (%d): %s", code, said)
	}
}

// snippet is as much of a body as belongs in an error message.
func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
