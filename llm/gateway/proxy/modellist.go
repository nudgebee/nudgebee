package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// filterModelList keeps the listed models allowed accepts (Gemini's "models/{id}" seen as {id}),
// keeping has_more and page tokens; a body that is not a list errors, so none is forwarded unfiltered.
func filterModelList(provider schemas.ModelProvider, body []byte, allowed func(id string) bool) ([]byte, error) {
	listKey, idKey := "data", "id"
	if provider == schemas.Gemini {
		listKey, idKey = "models", "name"
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	if top == nil {
		return nil, fmt.Errorf("model list is not an object")
	}
	var items []map[string]json.RawMessage
	raw, ok := top[listKey]
	switch {
	case !ok && provider == schemas.Gemini:
		// proto3 JSON omits an empty repeated field, so a page with no models has no "models".
	case !ok:
		return nil, fmt.Errorf("model list has no %q", listKey)
	default:
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
	}
	kept := make([]map[string]json.RawMessage, 0, len(items))
	for _, item := range items {
		var id string
		if json.Unmarshal(item[idKey], &id) != nil {
			continue
		}
		if provider == schemas.Gemini {
			id = strings.TrimPrefix(id, "models/")
		}
		if id != "" && allowed(id) {
			kept = append(kept, item)
		}
	}
	filtered, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	top[listKey] = filtered
	// Anthropic's page bounds are model ids, so they name the kept models or are dropped.
	if provider == schemas.Anthropic {
		for key, i := range map[string]int{"first_id": 0, "last_id": len(kept) - 1} {
			if _, sent := top[key]; sent {
				delete(top, key)
				if len(kept) > 0 {
					top[key] = kept[i][idKey]
				}
			}
		}
	}
	return json.Marshal(top)
}

// modelReadID is the model a GET on a provider's model collection reads, from the path that is
// forwarded. list is true for the collection itself; an id holding "/" names no single model.
func modelReadID(provider schemas.ModelProvider, path string) (id string, list bool) {
	collection := "/v1/models"
	if provider == schemas.Gemini {
		collection = "/models"
	}
	p := strings.TrimSuffix(path, "/")
	if strings.EqualFold(p, collection) {
		return "", true
	}
	if len(p) <= len(collection)+1 {
		return "", false
	}
	return p[len(collection)+1:], false
}
