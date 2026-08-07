package prompts

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PromptFileAPIVersion is the only schema version the loader accepts. Bumping it
// is a breaking change to every prompt file, so it is versioned explicitly rather
// than inferred.
const PromptFileAPIVersion = "nudgebee.dev/prompt/v1"

// PromptInput declares one load-time template variable. The set of inputs is the
// contract between a prompt file and its Go call site: every {{.var}} in the body
// must be declared here (or in RuntimeInputs), and the validator enforces it.
type PromptInput struct {
	Type     string `yaml:"type"`
	Required bool   `yaml:"required"`
	Default  any    `yaml:"default"`
}

// PromptFile is the on-disk YAML schema shared by every prompt, fragment and
// provider override. Identity comes from the file path, not from the fields:
// Name must equal the filename stem and Category the parent directory. Both are
// carried in the file only so a mismatch is caught by the validator — the same
// checked-redundancy role atlas.sum plays for migrations.
type PromptFile struct {
	APIVersion string         `yaml:"apiVersion"`
	Name       string         `yaml:"name"`
	Category   PromptCategory `yaml:"category"`

	// Inputs are rendered at load time and stay stable for a given agent role, so
	// they land inside the cacheable system prefix.
	Inputs map[string]PromptInput `yaml:"inputs"`

	// RuntimeInputs are left as literal {{.var}} for langchaingo to fill per
	// iteration. Declaring them separately is what stops the loader rendering a
	// placeholder that a later pass still needs.
	RuntimeInputs []string `yaml:"runtime_inputs"`

	// Includes is the manifest of fragments this prompt splices in. Placement is
	// the {{@include ...}} marker in Body; the validator asserts the two agree.
	Includes []string `yaml:"includes"`

	Body string `yaml:"body"`
}

// ParsePromptFile decodes and validates the structural invariants of a prompt
// file. Decoding is strict: an unknown or misspelled key is an error rather than
// a silently ignored field, since a dropped `inputs:` block would otherwise
// surface as an empty render at runtime.
func ParsePromptFile(data []byte) (*PromptFile, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var p PromptFile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decoding prompt file: %w", err)
	}

	if p.APIVersion != PromptFileAPIVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q (want %q)", p.APIVersion, PromptFileAPIVersion)
	}
	if strings.TrimSpace(p.Body) == "" {
		return nil, fmt.Errorf("body is empty")
	}

	return &p, nil
}
