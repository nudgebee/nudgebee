package core

import "github.com/tmc/langchaingo/llms"

// LLMModelDecorator is an OPTIONAL Enterprise-Edition hook that wraps every
// llms.Model GetLLMModel constructs. When set (typically by ee/scrubbing in
// its init() via a blank-import in cmd/main.go), the OSS factory passes each
// freshly constructed model through it before caching, so all ~31 call sites
// inherit the wrap for free.
//
// Singleton-assignment pattern, mirroring api-server/services/license
// (BootstrapCheckImpl = ...). We deliberately don't use a slice because only
// one wrapping layer is meaningful for a generative-LLM client — stacking
// decorators of unknown order is a footgun, and the EE caller can compose
// internally if it ever needs to layer behaviors.
//
// nil = OSS behavior, no decoration, zero runtime cost.
var LLMModelDecorator func(llms.Model) llms.Model
