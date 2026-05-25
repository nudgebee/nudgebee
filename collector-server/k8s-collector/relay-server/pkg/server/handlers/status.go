package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Status responds with a 200 OK and a plain‑text “OK” body.
func Status(c *gin.Context) {
	c.String(http.StatusOK, "OK")
}
