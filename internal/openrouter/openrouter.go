// Package openrouter puts a question to a model, through the gateway an account has a key
// for.
//
// Split from the store on the same seam as [btw/internal/mail]: the store decides what the
// companion *is* and holds its key, and this is the half that opens a socket. Nothing here
// touches the database.
//
// OpenRouter rather than a model vendor directly, because the choice of model is the
// account's and changing it should be a form field. One gateway, one credential, and the
// difference between two models is a string — which is what makes the model worth exposing
// at all.
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

// Endpoint is OpenRouter's chat completions route.
const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// DefaultModel is what an account gets without naming one.
//
// Free, so somebody who has only just found out this feature exists can try it without a
// balance, and it is what almost everybody will end up leaving it on. The `:free` variants
// accept far fewer parameters than the paid slug of the same model — no `structured_outputs`
// among them — so anything asking for a strict schema has to check rather than assume.
const DefaultModel = "minimax/minimax-m3:free"

// ErrRateLimited is a refusal that will pass on its own.
//
// A sentinel rather than a string somebody greps for, because the caller's response to it is
// categorically different from every other refusal here: a rejected key and a missing model
// want a person, and this wants a wait. A background loop that cannot tell them apart either
// hammers a quota it has already exhausted or gives up on a key that is fine.
var ErrRateLimited = errors.New("rate limited")

// Timeout caps one exchange.
//
// Long, because a reasoning model thinks before it answers and a free endpoint queues. It is
// still a cap: a gateway that has stopped answering must not hold a request open until the
// browser gives up first, because then nobody learns why.
const Timeout = 60 * time.Second

// endpoint is where requests go. A var only so a test can point it at a server it started
// itself — the same trick as rootCAs in internal/mail, and for the same reason: what is
// worth asserting is the conversation, not that net/http was called.
var endpoint = Endpoint

// SetEndpoint replaces where requests go and returns a function putting the old one back.
//
// For tests; the daemon never calls it. Exported because the handler that decides *which* key
// to try lives in another package, and the alternative was an api test that reached
// openrouter.ai for real — slow, offline-fragile, and testing somebody else's uptime.
func SetEndpoint(u string) func() {
	old := endpoint
	endpoint = u
	return func() { endpoint = old }
}

// Settings are the companion as somebody configured it. Carries the key.
type Settings struct {
	APIKey string
	Model  string

	// About is what the model is told about the person, in their own words.
	//
	// The whole reason a companion can say anything useful about when to raise a reminder.
	// A model that knows somebody sleeps until noon does not offer them a nine o'clock slot,
	// and nothing else in btw records that — a rhythm's waking window says which hours are
	// allowed, never which are wanted.
	About string
}

// Configured reports whether there is a companion to ask at all.
//
// The key alone. A model without a key cannot be reached, and a key without an About still
// answers — worse than it would with one, but the account has opted in either way.
func (s Settings) Configured() bool { return s.APIKey != "" }

// Result is what a successful exchange says about itself.
type Result struct {
	// Model is what actually served the request, which is not always what was asked for:
	// OpenRouter falls back between providers, and a slug can resolve to a variant. Worth
	// showing, because "you asked for X and Y answered" is a thing somebody wants to know
	// before they trust the answers.
	Model string

	// Tokens is what the exchange cost, so a test press says something about the next
	// thousand.
	Tokens int

	// Truncated is whether the model ran out of ceiling mid-answer.
	Truncated bool

	// reply is unexported so that [Check]'s caller cannot come to depend on the word the model
	// happened to say, while [Ask]'s gets it through a return value that means it.
	reply string
}

// Check asks the model to say one word, and reports what came back.
//
// A real completion rather than `GET /api/v1/key`, which would prove the key is live and
// nothing about the model. The model is the other half of what somebody typed, and it is the
// half they are likelier to get wrong — a slug with a dropped `:free`, a model that has been
// retired, one their key has no credit for. One completion proves both, and on the default
// model it costs nothing.
//
// The same argument as the relay's test send: an account setting this up gets it wrong two or
// three times, and each correction should be a form field and a press rather than a support
// question.
func Check(ctx context.Context, set Settings, via proxy.Settings) (Result, error) {
	if !set.Configured() {
		return Result{}, errors.New("there is no key to check")
	}

	body := map[string]any{
		"model": set.Model,
		// Small on purpose. This asks whether the gateway answers, not whether it answers
		// well, and a reasoning model's thinking counts against the same ceiling — so it is
		// generous enough that the reply is not cut off before it starts.
		"max_tokens": 200,
		"reasoning":  map[string]any{"exclude": true},
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with the single word: ok"},
		},
	}
	return post(ctx, set.APIKey, body, via)
}

// Ask puts a prompt to the configured model and returns what it said.
//
// The general form of [Check]: same credential, same error handling, a real answer wanted.
//
// JSON mode rather than a strict schema, because the default model is a `:free` variant and
// those advertise `response_format` without `structured_outputs` — asking for a schema they
// cannot honour gets prose back from a request that looked like it demanded otherwise. The
// caller parses leniently for the same reason.
func Ask(ctx context.Context, set Settings, via proxy.Settings, system, user string, maxTokens int) (string, Result, error) {
	if !set.Configured() {
		return "", Result{}, errors.New("there is no companion to ask")
	}

	body := map[string]any{
		"model":      set.Model,
		"max_tokens": maxTokens,
		// A reasoning model otherwise leaks its thinking into the content, which is the single
		// likeliest reason a JSON answer fails to parse.
		"reasoning": map[string]any{"exclude": true},
		// Low, not zero: this is extraction, not invention.
		"temperature": 0.2,
		// Fresh every time, and deliberately not a constant.
		//
		// A fixed seed would make the answer reproducible, which sounds like a virtue and is
		// the opposite of one here: the same question is asked again whenever anything changes,
		// and a model that put a reminder in the wrong half of the week would put it there
		// again, identically, forever. A new draw each time is the only thing that lets a bad
		// answer be replaced by a better one without the question itself changing.
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
		// Named rather than left to the parser, because a truncated answer is a torn-off JSON
		// object and "unexpected end of input" sends somebody looking at the prompt when the
		// fix is a bigger ceiling.
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

	// Through a proxy when one is configured and switched on, and straight out otherwise. The
	// decision is entirely proxy.Send's; nothing here needs to know which happened, which is
	// what keeps the two paths from drifting apart.
	resp, err := proxy.Send(ctx, req, via)
	if err != nil {
		return Result{}, fmt.Errorf("reach the gateway: %w", err)
	}
	defer resp.Body.Close()

	// Bounded, because this is a remote nobody here controls and a body that never ends
	// would hold the request open past the timeout that was supposed to bound it.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read the reply: %w", err)
	}

	var parsed completion
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// A gateway that answered with something other than JSON is worth quoting rather
		// than summarising: it is usually a proxy's error page, and the page says which.
		return Result{}, fmt.Errorf("%s: %s", resp.Status, snippet(raw))
	}

	// An error can arrive with a matching status or inside a 200 — OpenRouter answers 200
	// with the fault in the body when a provider dies after generating part of an answer, so
	// the status code alone is not the whole story.
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

// explain turns a status into the sentence somebody can act on.
//
// The gateway's own words go with it, never instead of it. "Unauthorized" and "this model
// wants credit" and "too many requests, wait a minute" are three different afternoons, and a
// single "that did not work" sends somebody to check the wrong thing first.
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
		// The one failure that is not a mistake. The free models allow twenty requests a
		// minute and fifty a day until credit has been bought, so a loop will meet this
		// honestly rather than through a bug.
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
