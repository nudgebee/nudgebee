package azureclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStreamingChatResponse_FinishReason(t *testing.T) {
	t.Parallel()
	mockBody := `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
	r := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(mockBody)),
	}

	req := &ChatRequest{
		StreamingFunc: func(_ context.Context, _ []byte) error {
			return nil
		},
	}

	resp, err := parseStreamingChatResponse(context.Background(), r, req)

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, FinishReason("stop"), resp.Choices[0].FinishReason)
}

func TestChatMessage_MarshalUnmarshal(t *testing.T) {
	t.Parallel()
	msg := ChatMessage{
		Role:    "assistant",
		Content: "hello",
		FunctionCall: &FunctionCall{
			Name:      "test",
			Arguments: "func",
		},
	}
	text, err := json.Marshal(msg)
	require.NoError(t, err)
	require.Equal(t, `{"role":"assistant","content":"hello","function_call":{"name":"test","arguments":"func"}}`, string(text)) // nolint: lll

	var msg2 ChatMessage
	err = json.Unmarshal(text, &msg2)
	require.NoError(t, err)
	require.Equal(t, msg, msg2)
}

func TestParseStreamingChatResponse_NoGoroutineLeakOnStreamingFuncError(t *testing.T) {
	// Emit many chunks so the producer goroutine is still trying to send when
	// the consumer aborts on the first one. Before the fix the producer blocks
	// forever on the unbuffered channel and the goroutine leaks; the fix cancels
	// it via context.
	var body strings.Builder
	for i := 0; i < 50; i++ {
		body.WriteString(`data: {"choices":[{"index":0,"delta":{"content":"x"}}]}` + "\n")
	}
	r := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body.String())),
	}
	req := &ChatRequest{
		StreamingFunc: func(_ context.Context, _ []byte) error {
			return errors.New("consumer aborted")
		},
	}

	// GC first so the baseline isn't skewed by transient runtime goroutines or
	// pending finalizers (keeps the count comparison stable in CI).
	runtime.GC()
	baseline := runtime.NumGoroutine()
	_, err := parseStreamingChatResponse(context.Background(), r, req)
	require.Error(t, err)

	// The producer goroutine must terminate; poll until the goroutine count
	// settles back to the baseline. Pre-fix it never does and this times out.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	runtime.GC()
	assert.LessOrEqual(t, runtime.NumGoroutine(), baseline,
		"parseStreamingChatResponse leaked a goroutine after the consumer returned early")
}
