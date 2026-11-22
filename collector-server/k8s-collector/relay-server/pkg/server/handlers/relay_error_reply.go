package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nudgebee/relay-server/pkg/models"
	"nudgebee/relay-server/pkg/utils"
)

// writeRelayErrorReply answers an HTTP proxy request whose reply is one the
// relay made itself — the request expired in the queue, the agent connection
// was lost, the agent's reply was too big. Those carry action "error" and a
// plain-text Data, not the HTTPResponse an agent sends back for a proxied
// call, so parsing them as one failed and every such failure reached the
// caller as a 500 "invalid HTTP response" instead of its real status.
// It reports whether ar was such a reply and has been answered.
func writeRelayErrorReply(c *gin.Context, ar models.AgentResponse) bool {
	if ar.Action != "error" {
		return false
	}
	status := ar.StatusCode
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	c.JSON(status, utils.BuildError(status, ar.Data))
	return true
}
