package core

import (
	"encoding/json"
	"fmt"
)

// ParseExtraHeaders parses a free-form "extra HTTP headers" config value: a JSON object
// of string values, e.g. {"Authorization": "Bearer <token>"}. An empty value means no
// headers.
//
// It lives in core so the integration descriptor that validates the field at save time
// and the query layer that parses it at request time run the same code. When those were
// two implementations, {"A": null} passed validation (encoding/json decodes a null into
// the empty string) and then failed at query time — the integration saved as valid and
// broke every query made through it.
func ParseExtraHeaders(raw string) (map[string]string, error) {
	headers := map[string]string{}
	if raw == "" {
		return headers, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf(`must be a JSON object of string values (e.g. {"Authorization": "Bearer <token>"})`)
	}
	for k, v := range parsed {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("value for header %q must be a string", k)
		}
		headers[k] = str
	}
	return headers, nil
}
