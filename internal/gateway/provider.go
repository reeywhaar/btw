package gateway

// Provider is which service a key is for.
//
// Two, and both speak OpenAI's chat completions, so what actually differs is the address, the
// model names and which optional fields are safe to send. An account's, not the instance's: a
// key works with one of them and not the other, and the key is somebody's own.
type Provider string

const (
	OpenRouter  Provider = "openrouter"
	HuggingFace Provider = "huggingface"
)

// DefaultProvider is what an account that has never said gets. Empty is not a third state here
// — unlike a model, where not choosing is a thing somebody means.
const DefaultProvider = OpenRouter

func Providers() []Provider { return []Provider{OpenRouter, HuggingFace} }

func (p Provider) Valid() bool { return p == OpenRouter || p == HuggingFace }

// OrDefault resolves the empty provider, so a row written before there were two still asks
// somewhere.
func (p Provider) OrDefault() Provider {
	if !p.Valid() {
		return DefaultProvider
	}
	return p
}

func (p Provider) Label() string {
	if p == HuggingFace {
		return "Hugging Face"
	}
	return "OpenRouter"
}

// KeysURL is where somebody goes to get one, which is the question the form is actually asking.
func (p Provider) KeysURL() string {
	if p == HuggingFace {
		return "huggingface.co/settings/tokens"
	}
	return "openrouter.ai/keys"
}

// KeyExample is what one of this service's keys looks like, for a placeholder.
func (p Provider) KeyExample() string {
	if p == HuggingFace {
		return "hf_…"
	}
	return "sk-or-v1-…"
}

func (p Provider) Endpoint() string {
	if p == HuggingFace {
		return "https://router.huggingface.co/v1/chat/completions"
	}
	return Endpoint
}

// DefaultModel is the compiled-in model for this provider, under an instance's own.
//
// A slug belongs to a provider — `minimax/minimax-m3:free` means nothing to the Hugging Face
// router — so there is one each rather than one shared.
// ProbeTarget is this service's one route that needs no key, for a proxy's test press.
func (p Provider) ProbeTarget() string {
	if p == HuggingFace {
		return "https://router.huggingface.co/v1/models"
	}
	return "https://openrouter.ai/api/v1/models"
}

func (p Provider) DefaultModel() string {
	if p == HuggingFace {
		return HuggingFaceModel
	}
	return DefaultModel
}

// tune adds what this provider alone accepts.
//
// Only OpenRouter is told not to think. Its models publish `reasoning` among their
// `supported_parameters`, so the field is asked for where it is known to be read.
//
// Nothing equivalent goes to the Hugging Face router. `chat_template_kwargs.thinking` is what
// its stack would read, and a model whose thinking mode is `required` refuses the whole request
// over it — a 400 rather than a field quietly ignored. Its `/v1/models` describes context,
// pricing, tools and structured output, and says nothing about thinking, so there is no way to
// send it only where it would be accepted. See [Provider.ThinkingBudget].
func (p Provider) tune(body map[string]any) {
	if p == OpenRouter {
		body["reasoning"] = map[string]any{"exclude": true}
	}
}

// MaxOutputTokens is the most any one answer may be given room for.
//
// Models cap what they will be asked for, and the cap is theirs rather than the router's:
// exceeding it is a 400 that names the model and refuses the request outright, not a shorter
// answer. 32,768 is the common one, and this sits under it.
//
// Nothing published says what a given model's cap is — the Hugging Face router's /v1/models
// carries context_length, which is the window and not this — so it is a constant low enough to
// be safe rather than a number read per model.
const MaxOutputTokens = 32000

// ThinkingBudget is the ceiling to allow for thinking that cannot be switched off.
//
// Thinking counts against max_tokens, so a model that must think and is given room only for an
// answer spends the lot and is cut off before writing any JSON. Zero for OpenRouter, where
// [Provider.tune] switches it off instead.
//
// Room rather than a refusal, because many models on the router think by default and the good
// ones among them are worth asking.
func (p Provider) ThinkingBudget() int {
	if p == HuggingFace {
		return 16000
	}
	return 0
}
