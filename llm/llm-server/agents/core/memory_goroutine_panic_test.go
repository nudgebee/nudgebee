package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

// A panic while composing memory must not take down the process (it runs on its
// own goroutine, out of reach of any caller's recover) or leave executeAgent
// waiting on the channel: the helper logs it and sends an empty value.
func TestComposeMemoryV2Into_RecoversPanic(t *testing.T) {
	original := ComposeMemoryV2BlockFn
	t.Cleanup(func() { ComposeMemoryV2BlockFn = original })
	ComposeMemoryV2BlockFn = func(*security.RequestContext, NBAgentRequest, NBAgent) MemoryPromptParts {
		panic("compose failed")
	}

	ch := make(chan MemoryPromptParts, 1)
	go composeMemoryV2Into(security.NewRequestContextForSuperAdmin(), ch, NBAgentRequest{}, nil)

	select {
	case got := <-ch:
		assert.Equal(t, MemoryPromptParts{}, got)
	case <-time.After(5 * time.Second):
		t.Fatal("no value sent after the panic; executeAgent would wait forever")
	}
}

func TestBuildMemoryNotebookInto_RecoversPanic(t *testing.T) {
	ch := make(chan string, 1)
	// A nil agent panics on its first method call.
	go buildMemoryNotebookInto(security.NewRequestContextForSuperAdmin(), ch, NBAgentRequest{}, nil, nil)

	select {
	case got := <-ch:
		assert.Empty(t, got)
	case <-time.After(5 * time.Second):
		t.Fatal("no value sent after the panic; executeAgent would wait forever")
	}
}
