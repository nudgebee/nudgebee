package api

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"nudgebee/runbook/common"
)

// publishVersionRequest is the body for POST /workflows/:id/publish.
// All fields optional. setLive defaults to true so the common case
// (publish + immediately use it) needs no body.
type publishVersionRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	SetLive     *bool   `json:"set_live,omitempty"`
}

// updateVersionMetadataRequest is the body for PATCH /workflows/:id/versions/:n.
// Nil pointer = leave column unchanged. "" = clear column.
type updateVersionMetadataRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (s *Server) listWorkflowVersions(c *gin.Context) {
	sc, accountID, ok := s.getRequestDetails(c)
	if !ok {
		return
	}
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow id is required"})
		return
	}

	versions, err := s.workflowService.ListWorkflowVersions(sc, accountID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to list workflow versions", "error", err, "workflow_id", id)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list workflow versions"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (s *Server) getWorkflowVersion(c *gin.Context) {
	sc, accountID, ok := s.getRequestDetails(c)
	if !ok {
		return
	}
	id := c.Param("id")
	versionNumber, err := strconv.Atoi(c.Param("version_number"))
	if err != nil || versionNumber <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_number must be a positive integer"})
		return
	}

	version, err := s.workflowService.GetWorkflowVersion(sc, accountID, id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to get workflow version", "error", err, "workflow_id", id, "version_number", versionNumber)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get workflow version"})
		return
	}
	c.JSON(http.StatusOK, version)
}

func (s *Server) restoreWorkflowVersion(c *gin.Context) {
	sc, accountID, ok := s.getRequestDetails(c)
	if !ok {
		return
	}
	id := c.Param("id")
	versionNumber, err := strconv.Atoi(c.Param("version_number"))
	if err != nil || versionNumber <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_number must be a positive integer"})
		return
	}

	wf, err := s.workflowService.RestoreWorkflowVersion(sc, accountID, id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to restore workflow version", "error", err, "workflow_id", id, "version_number", versionNumber)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore workflow version"})
		return
	}
	c.JSON(http.StatusOK, wf)
}

func (s *Server) publishWorkflowVersion(c *gin.Context) {
	sc, accountID, ok := s.getRequestDetails(c)
	if !ok {
		return
	}
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow id is required"})
		return
	}

	var req publishVersionRequest
	// Body is optional: an empty body means publish with no metadata and the
	// default set_live=true. ShouldBindJSON returns io.EOF for an empty body —
	// tolerate that, reject any other (malformed-JSON) error. ContentLength is
	// unreliable under chunked transfer encoding, so we don't gate on it.
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	setLive := true
	if req.SetLive != nil {
		setLive = *req.SetLive
	}

	v, err := s.workflowService.PublishWorkflow(sc, accountID, id, req.Name, req.Description, setLive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to publish workflow version", "error", err, "workflow_id", id)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish workflow version"})
		return
	}
	c.JSON(http.StatusOK, v)
}

func (s *Server) makeWorkflowVersionLive(c *gin.Context) {
	sc, accountID, ok := s.getRequestDetails(c)
	if !ok {
		return
	}
	id := c.Param("id")
	versionNumber, err := strconv.Atoi(c.Param("version_number"))
	if err != nil || versionNumber <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_number must be a positive integer"})
		return
	}

	wf, err := s.workflowService.SetLiveWorkflowVersion(sc, accountID, id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to set live workflow version", "error", err, "workflow_id", id, "version_number", versionNumber)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set live workflow version"})
		return
	}
	c.JSON(http.StatusOK, wf)
}

func (s *Server) updateWorkflowVersionMetadata(c *gin.Context) {
	sc, accountID, ok := s.getRequestDetails(c)
	if !ok {
		return
	}
	id := c.Param("id")
	versionNumber, err := strconv.Atoi(c.Param("version_number"))
	if err != nil || versionNumber <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_number must be a positive integer"})
		return
	}

	var req updateVersionMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	v, err := s.workflowService.UpdateWorkflowVersionMetadata(sc, accountID, id, versionNumber, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to update workflow version metadata", "error", err, "workflow_id", id, "version_number", versionNumber)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workflow version metadata"})
		return
	}
	c.JSON(http.StatusOK, v)
}
