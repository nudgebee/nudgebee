// Package edgeerr renders the gateway's OWN edge-rejection errors (rate limit,
// block, missing credential, bad token, bad request) in the ADDRESSED PROVIDER's
// native error envelope, so provider SDKs parse the status/retryability correctly.
//
// This matters because some SDKs — notably google-genai — read error.code and
// error.status from the response BODY and ignore the HTTP status line. A body that
// carries only a message makes a real 429 look like "Error 0" with empty status, and
// the SDK's retry classifier treats it as non-retryable and gives up. Carrying the
// status as error.code + a provider-native status/type string fixes every SDK
// pointed at the gateway (not just one client's classifier).
//
// It is a leaf package (gin + net/http only) so both the proxy and the auth
// middleware can render errors without an import cycle.
package edgeerr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Provider values match schemas.ModelProvider's string form.
const (
	Anthropic = "anthropic"
	OpenAI    = "openai"
	Gemini    = "gemini"
)

// Write aborts the request with an error body shaped like the addressed provider's
// native error. `code` is a short machine token (e.g. "rate_limit_exceeded",
// "model_not_allowed") used for OpenAI's error.code + the neutral fallback; `message`
// is human-readable. Already-set headers (e.g. Retry-After) are preserved.
func Write(c *gin.Context, provider string, status int, code, message string) {
	switch provider {
	case Gemini:
		// Google API error shape — SDKs read error.code (int) + error.status (the
		// google.rpc.Code name). Without these a 429 is dropped to "Error 0".
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
			"code":    status,
			"status":  geminiStatus(status),
			"message": message,
		}})
	case Anthropic:
		c.AbortWithStatusJSON(status, gin.H{
			"type":  "error",
			"error": gin.H{"type": anthropicType(status), "message": message},
		})
	case OpenAI:
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
			"message": message,
			"type":    openaiType(status),
			"code":    code,
			"param":   nil,
		}})
	default:
		// No addressed provider (e.g. an unknown mount) — a neutral gateway shape.
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": code, "message": message}})
	}
}

// geminiStatus maps an HTTP status to the google.rpc.Code name the genai SDK expects.
func geminiStatus(s int) string {
	switch s {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusRequestEntityTooLarge:
		return "INVALID_ARGUMENT"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "UNAVAILABLE"
	default:
		return "UNKNOWN"
	}
}

// anthropicType maps an HTTP status to Anthropic's error.type.
func anthropicType(s int) string {
	switch s {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// openaiType maps an HTTP status to OpenAI's error.type.
func openaiType(s int) string {
	switch s {
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusNotFound:
		return "invalid_request_error"
	default:
		return "api_error"
	}
}
