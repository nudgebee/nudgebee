package query

import (
	"fmt"
	"maps"
)

// NormalizeQueryRequestWhere rewrites request["query_request"]["where"] (the observability
// request shape) so that field conditions written without their `_binary` wrapper —
// {"namespace": {"_eq": "prod"}}, the Hasura style the generic query API, llm-server and
// runbook-server already accept — become `_binary` conditions ANDed with the rest. Before,
// the map decoder dropped such keys and the query ran unfiltered: Elasticsearch returned
// every line in the window as if the filter had applied.
//
// Already-wrapped clauses are left as they are. It errors only on what cannot be read as a
// clause: `_and`/`_or` that are not lists of objects, a `_not` or `_binary` that is not an
// object, or a field given both inside and outside `_binary`. A null `_and`/`_or`/`_not`/
// `_binary` is dropped, as the decoder did. Absent or non-object `where` values are left to
// the decoder.
func NormalizeQueryRequestWhere(request map[string]any) error {
	queryRequest, ok := request["query_request"].(map[string]any)
	if !ok {
		return nil
	}
	where, ok := queryRequest["where"].(map[string]any)
	if !ok {
		return nil
	}
	normalized, err := normalizeWhereClause(where, "query_request.where")
	if err != nil {
		return err
	}
	queryRequest["where"] = normalized
	return nil
}

func normalizeWhereClause(clause map[string]any, path string) (map[string]any, error) {
	out := make(map[string]any, len(clause))
	binary := map[string]any{}
	hasBinary := false
	if raw, ok := clause["_binary"]; ok && raw != nil {
		existing, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s._binary must be an object of field conditions", path)
		}
		maps.Copy(binary, existing)
		hasBinary = true
	}
	for key, value := range clause {
		switch key {
		case "_and", "_or", "_not", "_binary":
			if value == nil {
				continue // null decodes as an absent clause, as it did before
			}
		}
		switch key {
		case "_binary":
			// merged above
		case "_and", "_or":
			items, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("%s.%s must be a list of clauses", path, key)
			}
			normalized := make([]any, 0, len(items))
			for i, item := range items {
				sub, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s.%s[%d] must be a clause object", path, key, i)
				}
				n, err := normalizeWhereClause(sub, fmt.Sprintf("%s.%s[%d]", path, key, i))
				if err != nil {
					return nil, err
				}
				normalized = append(normalized, n)
			}
			out[key] = normalized
		case "_not":
			sub, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s._not must be a clause object", path)
			}
			n, err := normalizeWhereClause(sub, path+"._not")
			if err != nil {
				return nil, err
			}
			out[key] = n
		default:
			// A field condition outside _binary: move it in, as the generic query API does.
			if _, dup := binary[key]; dup {
				return nil, fmt.Errorf("field %q is given both inside and outside %s._binary", key, path)
			}
			binary[key] = value
		}
	}
	if hasBinary || len(binary) > 0 {
		out["_binary"] = binary
	}
	return out, nil
}
