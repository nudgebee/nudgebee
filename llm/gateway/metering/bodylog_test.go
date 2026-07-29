package metering

import (
	"strings"
	"testing"

	"nudgebee/llm-gateway/config"

	"github.com/stretchr/testify/assert"
)

func TestCapBody(t *testing.T) {
	prev := config.Config.BodyMaxBytes
	config.Config.BodyMaxBytes = 10
	t.Cleanup(func() { config.Config.BodyMaxBytes = prev })

	assert.Equal(t, "", CapBody(nil))
	assert.Equal(t, "", CapBody([]byte("")))
	assert.Equal(t, "short", CapBody([]byte("short")))

	over := CapBody([]byte("0123456789abcdef")) // 16 bytes, cap 10
	assert.True(t, strings.HasPrefix(over, "0123456789"))
	assert.Contains(t, over, "[truncated 6 bytes]")
}

func TestBodyLoggingEnabled(t *testing.T) {
	prevCap, prevURL := config.Config.CaptureBody, config.Config.GatewayDBURL
	t.Cleanup(func() {
		config.Config.CaptureBody, config.Config.GatewayDBURL = prevCap, prevURL
	})

	config.Config.CaptureBody = false
	config.Config.GatewayDBURL = "postgres://x"
	assert.False(t, BodyLoggingEnabled(), "off when flag is false")

	config.Config.CaptureBody = true
	config.Config.GatewayDBURL = ""
	config.Config.MeteringSink = config.SinkPostgres
	assert.False(t, BodyLoggingEnabled(), "off when no sink DB configured")

	config.Config.GatewayDBURL = "postgres://x"
	assert.True(t, BodyLoggingEnabled(), "on when flag set + DB configured")
}

func TestNewBodyLogSink_NoopWhenDisabled(t *testing.T) {
	prev := config.Config.CaptureBody
	config.Config.CaptureBody = false
	t.Cleanup(func() { config.Config.CaptureBody = prev })

	s := NewBodyLogSink()
	_, isNoop := s.(noopBodyLog)
	assert.True(t, isNoop, "disabled → noop sink")
	assert.NotPanics(t, func() { s.Record(BodyLog{ID: "x"}) })
	assert.NoError(t, s.Close())
}
