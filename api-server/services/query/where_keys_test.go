package query

import (
	"testing"

	"nudgebee/services/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeQueryRequestWhere(t *testing.T) {
	req := func(where any) map[string]any {
		return map[string]any{"account_id": "a", "query_request": map[string]any{"where": where}}
	}
	whereOf := func(r map[string]any) map[string]any {
		return r["query_request"].(map[string]any)["where"].(map[string]any)
	}
	cond := func(op string, v any) map[string]any { return map[string]any{op: v} }

	t.Run("wrapped clauses are unchanged", func(t *testing.T) {
		in := map[string]any{
			"_binary": map[string]any{"namespace": cond("_eq", "prod")},
			"_and": []any{
				map[string]any{"_binary": map[string]any{"pod": cond("_eq", "a")}},
				map[string]any{"_or": []any{map[string]any{"_binary": map[string]any{"level": cond("_eq", "error")}}}},
			},
			"_not": map[string]any{"_binary": map[string]any{"container": cond("_eq", "istio")}},
		}
		r := req(in)
		require.NoError(t, NormalizeQueryRequestWhere(r))
		assert.Equal(t, in, whereOf(r))
	})

	t.Run("unwrapped field conditions become _binary conditions", func(t *testing.T) {
		r := req(map[string]any{"namespace": cond("_eq", "prod"), "pod": cond("_eq", "a")})
		require.NoError(t, NormalizeQueryRequestWhere(r))
		assert.Equal(t, map[string]any{"_binary": map[string]any{
			"namespace": cond("_eq", "prod"), "pod": cond("_eq", "a"),
		}}, whereOf(r))
	})

	t.Run("unwrapped conditions merge with an existing _binary", func(t *testing.T) {
		r := req(map[string]any{"_binary": map[string]any{"namespace": cond("_eq", "prod")}, "pod": cond("_eq", "a")})
		require.NoError(t, NormalizeQueryRequestWhere(r))
		assert.Equal(t, map[string]any{"_binary": map[string]any{
			"namespace": cond("_eq", "prod"), "pod": cond("_eq", "a"),
		}}, whereOf(r))
	})

	t.Run("nested clauses are normalised too", func(t *testing.T) {
		r := req(map[string]any{
			"_and": []any{map[string]any{"pod": cond("_eq", "a")}},
			"_not": map[string]any{"level": cond("_eq", "debug")},
		})
		require.NoError(t, NormalizeQueryRequestWhere(r))
		assert.Equal(t, map[string]any{
			"_and": []any{map[string]any{"_binary": map[string]any{"pod": cond("_eq", "a")}}},
			"_not": map[string]any{"_binary": map[string]any{"level": cond("_eq", "debug")}},
		}, whereOf(r))
	})

	t.Run("no query_request, no where, empty where: unchanged", func(t *testing.T) {
		assert.NoError(t, NormalizeQueryRequestWhere(map[string]any{"account_id": "a"}))
		assert.NoError(t, NormalizeQueryRequestWhere(map[string]any{"query_request": map[string]any{}}))
		r := req(map[string]any{})
		require.NoError(t, NormalizeQueryRequestWhere(r))
		assert.Equal(t, map[string]any{}, whereOf(r))
	})

	t.Run("null clause keys are dropped, as the decoder did", func(t *testing.T) {
		r := req(map[string]any{"_and": nil, "_or": nil, "_not": nil, "_binary": nil, "pod": cond("_eq", "a")})
		require.NoError(t, NormalizeQueryRequestWhere(r))
		assert.Equal(t, map[string]any{"_binary": map[string]any{"pod": cond("_eq", "a")}}, whereOf(r))
	})

	for name, where := range map[string]map[string]any{
		"a field both inside and outside _binary": {"_binary": map[string]any{"pod": cond("_eq", "a")}, "pod": cond("_eq", "b")},
		"_and that is not a list":                 {"_and": map[string]any{"pod": cond("_eq", "a")}},
		"_and item that is not an object":         {"_and": []any{"pod"}},
		"_not that is not an object":              {"_not": []any{map[string]any{"pod": cond("_eq", "a")}}},
		"_binary that is not an object":           {"_binary": "pod=a"},
	} {
		t.Run("refused: "+name, func(t *testing.T) {
			assert.Error(t, NormalizeQueryRequestWhere(req(where)))
		})
	}
}

// The point of normalising: after it, the request decoder keeps the condition instead of
// dropping it.
func TestNormalizedWhereDecodesWithItsConditions(t *testing.T) {
	r := map[string]any{"query_request": map[string]any{"where": map[string]any{
		"namespace": map[string]any{"_eq": "prod"},
		"_and":      []any{map[string]any{"pod": map[string]any{"_eq": "a"}}},
	}}}
	require.NoError(t, NormalizeQueryRequestWhere(r))

	var decoded struct {
		QueryRequest struct {
			Where QueryWhereClause `mapstructure:"where"`
		} `json:"query_request"`
	}
	require.NoError(t, common.UnmarshalMapToStruct(r, &decoded))
	assert.Equal(t, "prod", decoded.QueryRequest.Where.Binary["namespace"]["_eq"])
	require.Len(t, decoded.QueryRequest.Where.And, 1)
	assert.Equal(t, "a", decoded.QueryRequest.Where.And[0].Binary["pod"]["_eq"])
}
