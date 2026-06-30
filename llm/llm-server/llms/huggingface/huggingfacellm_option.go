package huggingface

const (
	tokenEnvVarName = "HUGGINGFACEHUB_API_TOKEN"
	defaultModel    = "gpt2"
	defaultURL      = "https://api-inference.huggingface.co"
)

type options struct {
	token   string
	model   string
	url     string
	adapter string
	apiType string
}

type Option func(*options)

// WithToken passes the HuggingFace API token to the client. If not set, the token
// is read from the HUGGINGFACEHUB_API_TOKEN environment variable.
func WithToken(token string) Option {
	return func(opts *options) {
		opts.token = token
	}
}

// WithModel passes the HuggingFace model to the client. If not set, then will be
// used default model.
func WithModel(model string) Option {
	return func(opts *options) {
		opts.model = model
	}
}

func WithAdapter(adapter string) Option {
	return func(opts *options) {
		opts.adapter = adapter
	}
}

// WithURL passes the HuggingFace url to the client. If not set, then will be
// used default url.
func WithURL(url string) Option {
	return func(opts *options) {
		opts.url = url
	}
}

// WithAPIType selects the wire protocol. "openai" → POST {url}/v1/chat/completions
// with OpenAI chat payload (HF Dedicated Endpoints on vLLM, TGI 3.x, Ollama, vLLM,
// SGLang). Empty/anything else → legacy HF text-generation (TGI 2.x at root).
func WithAPIType(apiType string) Option {
	return func(opts *options) {
		opts.apiType = apiType
	}
}
