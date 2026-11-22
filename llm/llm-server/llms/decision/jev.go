package decision

import (
	"encoding/json"
	"fmt"
)

// Request shape shared by TypeSafe's Jev and Cloudflare's Clef: a "state" and a
// map of typed questions. Cloudflare wraps the answer in a Workers AI envelope.

type question struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria"`
}

type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type answers struct {
	Model   string            `json:"model"`
	Answers map[string]Choice `json:"answers"`
	Usage   struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// jevRequest builds the Jev / Clef body. An empty description is sent as null,
// which the API accepts.
func jevRequest(model, state, instructions string, options []Option) request {
	criteria := make(map[string]any, len(options))
	for _, o := range options {
		if o.Description == "" {
			criteria[o.Label] = nil
		} else {
			criteria[o.Label] = o.Description
		}
	}
	return request{Model: model, State: state, Questions: map[string]question{questionKey: {
		Type: "choice", Instructions: instructions, Criteria: criteria,
	}}}
}

// decodeJev reads a Jev / Clef answer. It accepts the Workers AI envelope
// ({"result": {...}, "success": true}) and a bare body.
func decodeJev(raw []byte) (Choice, error) {
	var parsed answers
	var envelope struct {
		Result  *answers `json:"result"`
		Success *bool    `json:"success"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		// Checked before Result: a Workers AI error has success=false and no result.
		if envelope.Success != nil && !*envelope.Success {
			return Choice{}, fmt.Errorf("provider reported success=false: %s", truncate(raw, 300))
		}
		if envelope.Result != nil {
			parsed = *envelope.Result
		}
	}
	if envelope.Result == nil {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return Choice{}, fmt.Errorf("decode response: %w", err)
		}
	}
	choice, ok := parsed.Answers[questionKey]
	if !ok || choice.Choice == "" {
		return Choice{}, fmt.Errorf("response has no answer")
	}
	choice.Model = parsed.Model
	choice.InputTokens = parsed.Usage.InputTokens
	return choice, nil
}
