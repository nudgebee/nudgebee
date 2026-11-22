package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getMqHealth(t *testing.T) (int, MqHealthResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health/mq", handleMqHealth)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/mq", nil))
	var body MqHealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body
}

func TestMqHealth_NoConsumersRegistered(t *testing.T) {
	common.MqClose()
	code, body := getMqHealth(t)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "degraded", body.Status)
}

// A consumer that can't reach the broker is ok within the grace window and 503 after it.
func TestMqHealth_StrandedConsumerTurns503AfterGrace(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedPort := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	savedGrace := mqHealthGrace
	savedHost, savedPort := config.Config.RabbitMqHost, config.Config.RabbitMqPort
	common.MqClose()
	t.Cleanup(func() {
		common.MqClose()
		mqHealthGrace = savedGrace
		config.Config.RabbitMqHost, config.Config.RabbitMqPort = savedHost, savedPort
	})
	config.Config.RabbitMqHost, config.Config.RabbitMqPort = "127.0.0.1", closedPort

	require.NoError(t, common.MqConsume("nbtest_health_ex", "nbtest_health_q", "nbtest_health_q", 1,
		func(context.Context, []byte) error { return nil }))

	mqHealthGrace = time.Hour
	code, body := getMqHealth(t)
	assert.Equal(t, http.StatusOK, code, "within grace")
	require.Len(t, body.Consumers, 1)
	assert.Equal(t, common.MqConsumerConnecting, body.Consumers[0].State)

	mqHealthGrace = 100 * time.Millisecond
	require.Eventually(t, func() bool {
		code, body = getMqHealth(t)
		return code == http.StatusServiceUnavailable
	}, 3*time.Second, 50*time.Millisecond, "stranded past grace must be 503")
	assert.Equal(t, "degraded", body.Status)
	assert.NotEmpty(t, body.Consumers[0].LastError)
}
