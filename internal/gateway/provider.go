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
// Both lines say the same thing — do not think — and they have to be said differently. A model
// that thinks spends the ceiling on thinking and is cut off before it writes any JSON, which
// arrives as "the answer was cut off" and looks like a budget that wants raising. Raising it
// buys a slower failure.
//
// `reasoning` is OpenRouter's own field, and the Hugging Face router would hand it to whichever
// provider is serving the model — where a rejected unknown field is a 400 nobody can explain
// from the message. `chat_template_kwargs` is what the Hugging Face stack reads instead: it
// reaches the chat template, which is where a hybrid model like DeepSeek's keeps the switch.
//
// Neither is load-bearing. A service that ignores its own is no worse off than before it was
// sent, and the truncation that follows says so in as many words.
func (p Provider) tune(body map[string]any) {
	if p == OpenRouter {
		body["reasoning"] = map[string]any{"exclude": true}
		return
	}
	body["chat_template_kwargs"] = map[string]any{"thinking": false}
}
