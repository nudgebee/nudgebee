package runtime

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Models our evals have run on, and the names each provider knows them by. The
// list is reviewed by hand; every name in it must be callable as written.
//
//go:embed eval_tested_models.json
var evalTestedModelsJSON []byte

// bedrockRegionPlaceholder in a Bedrock name is replaced by the cross-region
// inference prefix for the config's AWS region (us., eu., apac., ...).
const bedrockRegionPlaceholder = "{region}"

const evalTestedDateLayout = "2006-01-02"

type evalTestedProvider struct {
	Provider string   `json:"provider"`
	ModelIDs []string `json:"model_ids"`
	Tested   bool     `json:"tested"`
}

type evalTestedModel struct {
	Model        string               `json:"model"`
	DisplayName  string               `json:"display_name"`
	Decision     bool                 `json:"decision,omitempty"`
	DeprecatedOn string               `json:"deprecated_on,omitempty"`
	RetiresOn    string               `json:"retires_on,omitempty"`
	Providers    []evalTestedProvider `json:"providers"`
}

// EvalTestedModelName is one name the user can save in a config. Tested is
// false when the model was evaluated through another provider.
type EvalTestedModelName struct {
	Name   string `json:"name"`
	Tested bool   `json:"tested"`
}

type EvalTestedModelOption struct {
	Model        string                `json:"model"`
	DisplayName  string                `json:"display_name"`
	Deprecated   bool                  `json:"deprecated"`
	DeprecatedOn string                `json:"deprecated_on,omitempty"`
	RetiresOn    string                `json:"retires_on,omitempty"`
	Names        []EvalTestedModelName `json:"names"`
}

var loadEvalTestedModels = sync.OnceValues(func() ([]evalTestedModel, error) {
	var models []evalTestedModel
	if err := json.Unmarshal(evalTestedModelsJSON, &models); err != nil {
		return nil, fmt.Errorf("eval-tested models: %w", err)
	}
	return models, nil
})

// ListEvalTestedModels returns the eval-tested models a config for provider can
// use. Bedrock names get the prefix for region; models past their retirement
// date are left out. decision selects the decision-slot list instead of the
// main tiers.
func ListEvalTestedModels(provider, region string, decision bool, now time.Time) ([]EvalTestedModelOption, error) {
	models, err := loadEvalTestedModels()
	if err != nil {
		return nil, err
	}
	wantProvider := strings.ToLower(strings.TrimSpace(provider))
	today := now.UTC().Format(evalTestedDateLayout)
	options := make([]EvalTestedModelOption, 0, len(models))
	for _, m := range models {
		if m.Decision != decision || (m.RetiresOn != "" && m.RetiresOn <= today) {
			continue
		}
		var names []EvalTestedModelName
		for _, p := range m.Providers {
			if p.Provider != wantProvider {
				continue
			}
			for _, id := range p.ModelIDs {
				for _, name := range expandEvalTestedName(p.Provider, id, region) {
					names = append(names, EvalTestedModelName{Name: name, Tested: p.Tested})
				}
			}
		}
		if len(names) == 0 {
			continue
		}
		options = append(options, EvalTestedModelOption{
			Model:        m.Model,
			DisplayName:  m.DisplayName,
			Deprecated:   m.DeprecatedOn != "" && m.DeprecatedOn <= today,
			DeprecatedOn: m.DeprecatedOn,
			RetiresOn:    m.RetiresOn,
			Names:        names,
		})
	}
	return options, nil
}

func expandEvalTestedName(provider, id, region string) []string {
	if provider != "bedrock" || !strings.Contains(id, bedrockRegionPlaceholder) {
		return []string{id}
	}
	var names []string
	for _, prefix := range bedrockInferencePrefixes(region) {
		names = append(names, strings.Replace(id, bedrockRegionPlaceholder, prefix, 1))
	}
	return names
}

// bedrockInferencePrefixes returns the cross-region inference prefixes a
// Bedrock client in region can call. A prefix whose geography does not contain
// the region always fails, so only matching ones are offered, plus global.
// GovCloud has no global profiles.
func bedrockInferencePrefixes(region string) []string {
	r := strings.ToLower(strings.TrimSpace(region))
	switch {
	case strings.HasPrefix(r, "us-gov-"):
		return []string{"us-gov"}
	case strings.HasPrefix(r, "us-"):
		return []string{"us", "global"}
	case strings.HasPrefix(r, "eu-"):
		return []string{"eu", "global"}
	case strings.HasPrefix(r, "ca-"):
		return []string{"ca", "global"}
	case r == "ap-northeast-1" || r == "ap-northeast-3":
		return []string{"jp", "apac", "global"}
	case r == "ap-southeast-2" || r == "ap-southeast-4":
		return []string{"au", "apac", "global"}
	case strings.HasPrefix(r, "ap-"):
		return []string{"apac", "global"}
	default:
		return []string{"global"}
	}
}
