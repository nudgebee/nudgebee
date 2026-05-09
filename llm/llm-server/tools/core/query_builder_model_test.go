package core

import (
	"context"
	"io"
	"log/slog"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildLogQueryBuilder_CommandUnwrap(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	toolCtx := NbToolContext{Ctx: reqCtx}

	t.Run("command wrapper with where and start_time", func(t *testing.T) {
		// Real failure pattern from DB: LLM wraps query in "command" key
		input := `{"command": {"where": {"pod": {"_eq": "api-server"}}}, "start_time": "2024-01-01T10:00:00Z"}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
		assert.Equal(t, "2024-01-01T10:00:00Z", qb.StartTime, "start_time should be preserved after unwrap")
	})

	t.Run("command wrapper with where only", func(t *testing.T) {
		input := `{"command": {"where": {"namespace": {"_eq": "demo"}}}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
	})

	t.Run("command wrapper with range", func(t *testing.T) {
		// From DB: {"command": {"where": {"app":{"_eq":"my-app"}}}, "range": "1h"}
		input := `{"command": {"where": {"app": {"_eq": "my-app"}}}, "range": "1h"}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
		assert.Equal(t, "1h", qb.TimeRange, "range should be preserved after unwrap")
	})

	t.Run("normal query without command wrapper", func(t *testing.T) {
		input := `{"where": {"pod": {"_eq": "api-server"}}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
	})

	t.Run("both command and where at top level — where takes priority", func(t *testing.T) {
		input := `{"command": {"where": {"pod": {"_eq": "wrong"}}}, "where": {"pod": {"_eq": "correct"}}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
		// "where" at top level should win — command should NOT be unwrapped
	})

	t.Run("command is a string not a map — no crash", func(t *testing.T) {
		input := `{"command": "show me logs", "where": {"namespace": {"_eq": "demo"}}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
	})

	t.Run("command is null — no crash", func(t *testing.T) {
		input := `{"command": null, "where": {"namespace": {"_eq": "demo"}}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
	})

	t.Run("top-level start_time wins over command start_time", func(t *testing.T) {
		input := `{"command": {"where": {"pod": {"_eq": "api"}}, "start_time": "2024-01-01T00:00:00Z"}, "start_time": "2024-06-15T10:00:00Z"}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.NotNil(t, qb.Where)
		assert.Equal(t, "2024-06-15T10:00:00Z", qb.StartTime, "top-level start_time should win over command's start_time")
	})
}

func TestBuildLogQueryBuilder_IndexField(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	toolCtx := NbToolContext{Ctx: reqCtx}

	t.Run("index at top level", func(t *testing.T) {
		input := `{"where": {"message": {"_ilike": "%error%"}}, "index": "app-logs-*"}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.Equal(t, "app-logs-*", qb.Index)
	})

	t.Run("index inside command wrapper", func(t *testing.T) {
		input := `{"command": {"where": {"message": {"_ilike": "%error%"}}, "index": "nginx-access-*"}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.Equal(t, "nginx-access-*", qb.Index)
	})

	t.Run("no index field — defaults to empty", func(t *testing.T) {
		input := `{"where": {"pod": {"_eq": "api-server"}}}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.Empty(t, qb.Index)
	})

	t.Run("empty string index — treated as no index", func(t *testing.T) {
		input := `{"where": {"pod": {"_eq": "api-server"}}, "index": ""}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.Empty(t, qb.Index)
	})

	t.Run("null index — no crash", func(t *testing.T) {
		input := `{"where": {"pod": {"_eq": "api-server"}}, "index": null}`
		qb, err := BuildLogQueryBuilder(toolCtx, input)
		assert.NoError(t, err)
		assert.Empty(t, qb.Index)
	})
}
