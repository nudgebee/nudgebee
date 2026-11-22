package decision

import (
	"encoding/json"
	"fmt"
)

// OpenAI Decisions (POST /v1/decisions): an "input" and an array of named
// questions; choices keep the caller's order.

type openaiChoice struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type openaiQuestion struct {
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	Instructions string         `json:"instructions"`
	Choices      []openaiChoice `json:"choices"`
}

type openaiRequest struct {
	Model     string           `json:"model"`
	Input     string           `json:"input"`
	Questions []openaiQuestion `json:"questions"`
}

type openaiResponse struct {
	Model   string `json:"model"`
	Answers []struct {
		Type          string   `json:"type"`
		Name          string   `json:"name"`
		Choice        string   `json:"choice"`
		Confidence    *float64 `json:"confidence"`
		Probabilities []struct {
			Value       string  `json:"value"`
			Probability float64 `json:"probability"`
		} `json:"probabilities"`
	} `json:"answers"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// openaiRequestFor builds the OpenAI Decisions body.
func openaiRequestFor(model, state, instructions string, options []Option) openaiRequest {
	choices := make([]openaiChoice, 0, len(options))
	for _, o := range options {
		choices = append(choices, openaiChoice{Value: o.Label, Description: o.Description})
	}
	return openaiRequest{Model: model, Input: state, Questions: []openaiQuestion{{
		Type: "choice", Name: questionKey, Instructions: instructions, Choices: choices,
	}}}
}

// decodeOpenAI reads an OpenAI Decisions answer. A refusal is an answer, not an
// error: the caller treats it like a low-confidence one.
func decodeOpenAI(raw []byte) (Choice, error) {
	var parsed openaiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Choice{}, fmt.Errorf("decode response: %w", err)
	}
	for _, a := range parsed.Answers {
		if a.Name != questionKey {
			continue
		}
		choice := Choice{Model: parsed.Model, InputTokens: parsed.Usage.InputTokens}
		if a.Type == "refusal" {
			choice.Refused = true
			return choice, nil
		}
		if a.Choice == "" {
			break
		}
		choice.Choice, choice.Confidence = a.Choice, a.Confidence
		choice.Probabilities = make(map[string]float64, len(a.Probabilities))
		for _, p := range a.Probabilities {
			choice.Probabilities[p.Value] = p.Probability
		}
		return choice, nil
	}
	return Choice{}, fmt.Errorf("response has no answer")
}
