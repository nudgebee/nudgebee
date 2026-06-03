package utils

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nudgebee/relay-server/pkg/models"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// fallbackClient is a shared HTTP client with a timeout for all fallback requests.
var fallbackClient = &http.Client{Timeout: 30 * time.Second}

// SerializeHTTPRequest turns an incoming *http.Request into your HTTPRequest model.
// It strips the "/grafana" prefix, reads the body, and base64‑encodes it.
// Uses a copy of the URL to avoid modifying the original request.
// Returns an error if the request body cannot be read.
func SerializeHTTPRequest(req *http.Request) (r *models.HTTPRequest, err error) {
	r = new(models.HTTPRequest)

	// Create a copy of the URL to avoid modifying the original request
	urlCopy := *req.URL
	// remove /grafana from the URL path
	urlCopy.Path = strings.TrimPrefix(urlCopy.Path, "/grafana")
	r.URL = urlCopy.String()
	r.Method = req.Method
	r.Header = req.Header
	r.ContentLength = req.ContentLength

	// convert req.Body to base64
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	encodedBody := base64.StdEncoding.EncodeToString(buf.Bytes())
	r.Body = encodedBody
	return r, nil
}

// SerializePrometheusHTTPRequest turns an incoming *http.Request into your HTTPRequest model.
// It strips the "/prometheus-v2" prefix, reads the body, and base64‑encodes it.
// Adds X-NB-Request-Type header to identify as Prometheus request.
// Uses a copy of the URL to avoid modifying the original request.
// Returns an error if the request body cannot be read.
func SerializePrometheusHTTPRequest(req *http.Request) (r *models.HTTPRequest, err error) {
	r = new(models.HTTPRequest)

	// Create a copy of the URL to avoid modifying the original request
	urlCopy := *req.URL
	// remove /prometheus-v2 from the URL path
	urlCopy.Path = strings.TrimPrefix(urlCopy.Path, "/prometheus-v2")
	r.URL = urlCopy.String()
	r.Method = req.Method
	r.Header = req.Header
	r.ContentLength = req.ContentLength

	// Add header to identify this as a Prometheus request
	if r.Header == nil {
		r.Header = make(map[string][]string)
	}
	r.Header["X-NB-Request-Type"] = []string{"Prometheus"}

	// convert req.Body to base64
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	encodedBody := base64.StdEncoding.EncodeToString(buf.Bytes())
	r.Body = encodedBody
	return r, nil
}

// SerializeAPIProxyRequest turns an incoming *http.Request into your HTTPRequest model.
// It strips the "/api/proxy" prefix, reads the body, and base64‑encodes it.
// This is a generic API proxy handler for any HTTP request forwarding.
// Uses a copy of the URL to avoid modifying the original request.
// Returns an error if the request body cannot be read.
func SerializeAPIProxyRequest(req *http.Request) (r *models.HTTPRequest, err error) {
	r = new(models.HTTPRequest)

	// Create a copy of the URL to avoid modifying the original request
	urlCopy := *req.URL
	// remove /api/proxy from the URL path
	urlCopy.Path = strings.TrimPrefix(urlCopy.Path, "/api/proxy")
	r.URL = urlCopy.String()
	r.Method = req.Method
	r.Header = req.Header
	r.ContentLength = req.ContentLength

	// Add header to identify this as an API proxy request
	if r.Header == nil {
		r.Header = make(map[string][]string)
	}
	r.Header["X-NB-Request-Type"] = []string{"APIProxy"}

	// convert req.Body to base64
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	encodedBody := base64.StdEncoding.EncodeToString(buf.Bytes())
	r.Body = encodedBody
	return r, nil
}

func FallbackPost(c *gin.Context, logger slog.Logger, agentURL string, payload interface{}) {
	endpoint := fmt.Sprintf("%s/api/trigger/action", agentURL)

	// 1) Forward the request to the agent
	bodyBytes, _ := json.Marshal(payload)
	resp, err := fallbackClient.Post(endpoint, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		logger.Error("relay: failed to call agent HTTP", "error", err)
		c.JSON(http.StatusBadGateway, BuildError(http.StatusBadGateway, "failed to call agent HTTP"))
		return
	}
	defer resp.Body.Close() // nolint:errcheck

	// 2) Read the response body
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("relay: failed to read agent response", "error", err)
		c.JSON(http.StatusBadGateway, BuildError(http.StatusBadGateway, "failed to read agent response"))
		return
	}

	// 3) (Optional) transform the response JSON
	var envelope map[string]interface{}
	if err := json.Unmarshal(respBytes, &envelope); err == nil {
		if data, ok := envelope["data"].(map[string]interface{}); ok {
			// only return the inner "data" field
			respBytes, _ = json.Marshal(data)
		}
	} else {
		logger.Warn("relay: unable to parse agent response JSON, forwarding raw", "error", err)
		// leave respBytes as-is
	}

	// 4) Propagate upstream status and headers in one shot
	c.Data(resp.StatusCode, "application/json", respBytes)
}

// FallbackPostWS posts `payload` to agentURL+"/api/trigger/action"
// and writes the raw HTTP response back over the provided WS.
func FallbackPostWS(ws *websocket.Conn, agentURL string, payload interface{}) {
	endpoint := fmt.Sprintf("%s/api/trigger/action", agentURL)
	body, _ := json.Marshal(payload)
	resp, err := fallbackClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		err := ws.WriteJSON(BuildError(502, "failed to call agent HTTP"))
		if err != nil {
			return
		}
		return
	}
	defer resp.Body.Close() // nolint:errcheck
	data, _ := io.ReadAll(resp.Body)
	err = ws.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return
	}
}

// BuildError is a helper to create a one‑line ErrorEnvelope.
func BuildError(code int, message string) *models.ErrorEnvelope {
	return &models.ErrorEnvelope{
		Errors: []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{
			{Code: code, Message: message},
		},
	}
}

func Decrypt(encryptionKey string, encryptedMsg string) (string, error) {
	key, err := hex.DecodeString(encryptionKey)
	if err != nil {
		return "", err
	}

	data, err := hex.DecodeString(encryptedMsg)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	nonceSize := 12
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	dec, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(dec), nil
}

func BuildContextFromPayload(c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*slog.Logger, *trace.Tracer, *metric.Meter) {
	var ctx context.Context
	if c != nil && c.Request != nil && c.Request.Context() != nil {
		ctx = c.Request.Context()
	} else {
		ctx = context.Background()
	}
	span := trace.SpanFromContext(ctx)
	acctIfc, _ := c.Get("accountID")

	traceId := span.SpanContext().TraceID()
	childLogger := logger.With("account_id", acctIfc, "trace_id", traceId.String())
	return childLogger, tracer, meter
}

func FallbackGrafana(
	c *gin.Context,
	logger *slog.Logger,
	agentURL string,
	httpReq models.HTTPRequest,
) {
	// 1) Send HTTP POST to the agent’s Grafana endpoint
	endpoint := fmt.Sprintf("%s/api/trigger/action", agentURL)
	bodyBytes, _ := json.Marshal(httpReq)

	resp, err := fallbackClient.Post(endpoint, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		logger.Error("relay: failed to call agent HTTP for Grafana", "error", err)
		c.JSON(http.StatusBadGateway, BuildError(http.StatusBadGateway, "failed to call agent HTTP"))
		return
	}
	defer resp.Body.Close() // nolint:errcheck

	// 2) Read response
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("relay: failed to read Grafana agent response", "error", err)
		c.JSON(http.StatusBadGateway, BuildError(http.StatusBadGateway, "failed to read agent response"))
		return
	}

	// 3) Unwrap AgentResponse
	var agentResp models.AgentResponse
	if err := json.Unmarshal(respBytes, &agentResp); err != nil {
		logger.Error("relay: invalid AgentResponse JSON", "error", err)
		c.JSON(http.StatusInternalServerError, BuildError(http.StatusInternalServerError, "invalid agent response"))
		return
	}

	// 4) Unwrap inner HTTPResponse
	var hr models.HTTPResponse
	if err := json.Unmarshal([]byte(agentResp.Data), &hr); err != nil {
		logger.Error("relay: invalid HTTPResponse JSON", "error", err)
		c.JSON(http.StatusInternalServerError, BuildError(http.StatusInternalServerError, "invalid HTTP response"))
		return
	}

	// 5) Propagate headers with support for multi-value headers
	for k, values := range hr.Header {
		for _, v := range values {
			c.Header(k, v)
		}
	}

	// 6) Decode base64 body
	decoded, err := base64.StdEncoding.DecodeString(hr.Body)
	if err != nil {
		logger.Error("relay: failed to decode response body", "error", err)
		c.JSON(http.StatusInternalServerError, BuildError(http.StatusInternalServerError, "invalid body encoding"))
		return
	}
	if _, err = c.Writer.Write(decoded); err != nil {
		logger.Error("relay: Failed to write response body", "error", err)
		c.JSON(400, BuildError(400, "Failed to send request to agent server"))
	}

}

func FallbackWS(c *gin.Context, logger slog.Logger, agentURL string, payload models.InteractiveShellRequest) {
	endpoint := fmt.Sprintf("%s/api/terminal", agentURL)

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to marshal payload", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		logger.Error("Failed to create proxy request", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// If your clients send auth tokens, forward them too
	if auth := c.GetHeader("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := fallbackClient.Do(req)
	if err != nil {
		logger.Error("Error forwarding to agent", "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "bad gateway"})
		return
	}
	defer resp.Body.Close() // nolint:errcheck

	// 5) Copy response headers
	for k, vals := range resp.Header {
		for _, v := range vals {
			c.Writer.Header().Add(k, v)
		}
	}

	// 6) Read and write the body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read agent response", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// 7) Return status + body
	c.Status(resp.StatusCode)
	_, err = c.Writer.Write(respBody)
	if err != nil {
		logger.Error("Failed to write response body", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send response"})
		return
	}
}
