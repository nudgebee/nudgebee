package metering

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockSink wires a dbSink onto a sqlmock-backed DatabaseManager and starts its
// background goroutine, returning the sink and the mock for expectations.
func newMockSink(t *testing.T) (*dbSink, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "postgres")
	s := &dbSink{
		db:   &common.DatabaseManager{Db: sqlxDB},
		ch:   make(chan UsageEvent, sinkBufferSize),
		done: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run()
	return s, mock
}

func sampleEvent() UsageEvent {
	return NewEvent(EventInput{
		Provider:   schemas.Anthropic,
		Model:      "claude-haiku-4-5",
		Method:     "POST",
		Path:       "/v1/messages",
		StatusCode: 200,
		LatencyMS:  1200,
		Headers:    map[string]string{"request-id": "req_test"},
		Usage: &schemas.BifrostPassthroughUsage{
			LLMUsage: &schemas.BifrostLLMUsage{PromptTokens: 14, CompletionTokens: 7, TotalTokens: 21},
		},
	})
}

func TestDBSink_RecordInsertsRow(t *testing.T) {
	s, mock := newMockSink(t)

	// One row → one multi-row INSERT into llm_gateway_usage with all columns.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_gateway_usage")).
		WithArgs(
			sqlmock.AnyArg(),   // id
			sqlmock.AnyArg(),   // created_at
			"", "", "", "", "", // identity (empty until auth)
			"", sqlmock.AnyArg(), // session_id, attributes (JSON document)
			"anthropic", "claude-haiku-4-5", // provider, model (resolved)
			"", "", "", "", "", // requested_provider, requested_model, responded_model, routing_reason, routing_rule
			"POST", "/v1/messages", false,
			200, int64(1200), "req_test",
			14, 7, 21, 0, 0, "",
			0.05, // cost_usd — snapshotted at write time
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ev := sampleEvent()
	ev.CostUsd = 0.05
	s.Record(ev)
	require.NoError(t, s.Close()) // drains + flushes before returning

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDBSink_BatchesMultipleRows(t *testing.T) {
	s, mock := newMockSink(t)

	// Three events buffered then flushed on Close → a single batched INSERT.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_gateway_usage")).
		WillReturnResult(sqlmock.NewResult(0, 3))

	for range 3 {
		s.Record(sampleEvent())
	}
	require.NoError(t, s.Close())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDBSink_FlushesOnTicker(t *testing.T) {
	s, mock := newMockSink(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_gateway_usage")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	s.Record(sampleEvent())
	// Do NOT close; wait for the periodic flush (batchMaxWait) to fire.
	require.Eventually(t, func() bool {
		return mock.ExpectationsWereMet() == nil
	}, batchMaxWait+2*time.Second, 50*time.Millisecond)

	_ = s.Close()
}

func TestDBSink_RecordNeverBlocks(t *testing.T) {
	// A full buffer must drop, not block the request path.
	s := &dbSink{
		db:   &common.DatabaseManager{},
		ch:   make(chan UsageEvent, 1),
		done: make(chan struct{}),
	}
	// No goroutine draining ch → second Record must fall through the default case.
	s.Record(sampleEvent())
	done := make(chan struct{})
	go func() {
		s.Record(sampleEvent())
		s.Record(sampleEvent())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record blocked when buffer was full")
	}
	assert.GreaterOrEqual(t, s.dropped.Load(), int64(1))
}

func TestNoopSink(t *testing.T) {
	var s Sink = noopSink{}
	assert.NotPanics(t, func() { s.Record(sampleEvent()) })
	assert.NoError(t, s.Close())
}

// TestDBSink_ConnectAbortsOnClose locks review Finding 6: sink init is async, so a
// never-ready DB must not block — Record stays non-blocking and Close returns
// promptly (the background connect loop aborts on done) without a real DB. Points
// at a refused port so each connect attempt fails fast and deterministically.
func TestDBSink_ConnectAbortsOnClose(t *testing.T) {
	prevURL, prevSink := config.Config.GatewayDBURL, config.Config.MeteringSink
	config.Config.GatewayDBURL = "postgres://u@127.0.0.1:1/db?sslmode=disable"
	config.Config.MeteringSink = config.SinkPostgres
	t.Cleanup(func() {
		config.Config.GatewayDBURL, config.Config.MeteringSink = prevURL, prevSink
	})

	s := &dbSink{
		ch:   make(chan UsageEvent, sinkBufferSize),
		done: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run() // connect() fails fast (refused) and backs off, not blocking

	// Requests keep flowing (buffered) while the DB isn't up.
	assert.NotPanics(t, func() {
		for range 5 {
			s.Record(sampleEvent())
		}
	})

	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked — async connect did not abort on done")
	}
}

func TestNewEvent_CarriesAPIKeyID(t *testing.T) {
	ev := NewEvent(EventInput{
		Provider: "openai", Model: "gpt-5", Method: "POST", Path: "/v1/chat/completions",
		TenantID: "t", UserID: "u", TokenID: "claude-code", APIKeyID: "key-uuid-1",
	})
	assert.Equal(t, "key-uuid-1", ev.APIKeyID)
}

func TestUsageColumnsIncludeAPIKeyID(t *testing.T) {
	assert.Contains(t, usageColumns, "api_key_id")
}

// Ticket #40089: a NUL in one event made Postgres refuse the whole batch insert.
func TestSanitize_StripsWhatPostgresRefuses(t *testing.T) {
	ev := sampleEvent()
	ev.Model = "gpt\x00x"
	ev.Path = "/v1/\xffmessages"
	ev.Attributes = `{"derived":{"reject_reason":"a\u0000b"}}`

	got := sanitize(ev)

	assert.Equal(t, "gptx", got.Model)
	assert.Equal(t, "/v1/�messages", got.Path)
	assert.JSONEq(t, `{"derived":{"reject_reason":"ab"}}`, got.Attributes)
}

// Review of #40089: a client can send the literal text \u0000 (no NUL). Stripping it from the
// marshalled JSON used to eat the escaped backslash before it and leave invalid JSON.
func TestSanitize_KeepsEscapedBackslashBeforeU0000Text(t *testing.T) {
	ev := sampleEvent()
	ev.Attributes = marshalAttributes(&Attributes{Actual: map[string]any{
		"tools": []string{`get_weather\u0000`, "nul\x00here"},
	}})

	got := sanitize(ev)

	require.True(t, json.Valid([]byte(got.Attributes)), got.Attributes)
	assert.JSONEq(t, `{"actual":{"tools":["get_weather\\u0000","nulhere"]}}`, got.Attributes)
}

// A batch Postgres refuses is retried row by row, so only the bad row is lost.
func TestDBSink_RefusedBatchRetriesRowByRow(t *testing.T) {
	s, mock := newMockSink(t)
	insert := regexp.QuoteMeta("INSERT INTO llm_gateway_usage")
	refused := &pq.Error{Code: "22P05", Message: "unsupported Unicode escape sequence"}
	mock.ExpectExec(insert).WillReturnError(refused)
	mock.ExpectExec(insert).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(insert).WillReturnError(refused)
	mock.ExpectExec(insert).WillReturnResult(sqlmock.NewResult(0, 1))

	for range 3 {
		s.Record(sampleEvent())
	}
	require.NoError(t, s.Close())
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An error that is not Postgres refusing a row (DB unreachable) is not retried per row.
func TestDBSink_UnreachableDBDoesNotRetryPerRow(t *testing.T) {
	s, mock := newMockSink(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_gateway_usage")).
		WillReturnError(errors.New("dial tcp: connection refused"))

	for range 3 {
		s.Record(sampleEvent())
	}
	require.NoError(t, s.Close())
	assert.NoError(t, mock.ExpectationsWereMet())
}
