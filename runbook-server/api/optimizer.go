package api

import (
	"net/http"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/optimizer"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func optimizationEnabledMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Config.OptimizationEnabled {
			c.JSON(http.StatusForbidden, gin.H{"error": "optimization feature is disabled"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) getAutoOptimizerRecommendation(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}

	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	var input struct {
		RuleName   string   `json:"rule_name" mapstructure:"rule_name"`
		Status     []string `json:"status" mapstructure:"status"`
		Categories []string `json:"categories" mapstructure:"categories"`
	}

	if err := common.DecodeMapToStruct(args, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	var ruleName *string
	if input.RuleName != "" {
		ruleName = &input.RuleName
	}

	resp, err := s.optimizerService.GetRecommendations(c.Request.Context(), sc, accUUID, ruleName, input.Status, input.Categories)
	if err != nil {
		s.logger.Error("failed to get recommendations", "error", err)
		handleServiceError(c, err, "failed to get recommendations")
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) getAutoOptimizerWorkload(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}
	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	var input struct {
		Status         string                             `json:"status" mapstructure:"status"`
		ResourceFilter []model.AutoOptimizeResourceFilter `json:"resource_filter" mapstructure:"resource_filter"`
	}

	if err := common.DecodeMapToStruct(args, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	if len(input.ResourceFilter) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resource filter is required"})
		return
	}

	var status *string
	if input.Status != "" {
		status = &input.Status
	}

	ids, err := s.optimizerService.GetWorkload(c.Request.Context(), sc, accUUID, status, input.ResourceFilter)
	if err != nil {
		s.logger.Error("failed to get workload", "error", err)
		handleServiceError(c, err, "failed to get workload")
		return
	}

	c.JSON(http.StatusOK, ids)
}

func (s *Server) autoPilotExecutionSkip(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}
	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized to skip execution"})
		return
	}

	var input struct {
		ID        string `json:"id" mapstructure:"id"`
		ByMinutes int    `json:"by_minutes" mapstructure:"by_minutes"`
		Platform  string `json:"platform" mapstructure:"platform"`
	}

	if err := common.DecodeMapToStruct(args, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	if input.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID not found"})
		return
	}

	aoID, err := uuid.Parse(input.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auto optimize id"})
		return
	}

	msg, err := s.optimizerService.SkipExecution(c.Request.Context(), sc, accUUID, aoID, input.ByMinutes)
	if err != nil {
		s.logger.Error("failed to skip execution", "error", err)
		handleServiceError(c, err, "failed to skip execution")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": msg})
}

func (s *Server) autoOptimizerUpdate(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}
	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized to update auto optimize"})
		return
	}

	var req model.AutoOptimizeRequestModel
	if err := common.DecodeMapToStruct(args, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	if err := common.ValidateStruct(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed: " + err.Error()})
		return
	}

	if req.ID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id missing for update"})
		return
	}

	req.AccountID = accUUID

	id, err := s.optimizerService.UpdateAutoOptimize(c.Request.Context(), sc, accUUID, req)
	if err != nil {
		s.logger.Error("failed to update auto optimize", "error", err)
		handleServiceError(c, err, "failed to update auto optimize")
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

func (s *Server) autoOptimizerCreate(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}
	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeCreate) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized to create auto optimize"})
		return
	}

	var req model.AutoOptimizeRequestModel
	if err := common.DecodeMapToStruct(args, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	if err := common.ValidateStruct(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed: " + err.Error()})
		return
	}

	req.AccountID = accUUID

	id, err := s.optimizerService.CreateAutoOptimize(c.Request.Context(), sc, accUUID, req)
	if err != nil {
		s.logger.Error("failed to create auto optimize", "error", err)
		handleServiceError(c, err, "failed to create auto optimize")
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

func (s *Server) autoOptimizerChangeStatus(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}
	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized to change status"})
		return
	}

	var input struct {
		ID     string `json:"id" mapstructure:"id"`
		Status string `json:"status" mapstructure:"status"`
	}

	if err := common.DecodeMapToStruct(args, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	if input.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if input.Status == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status is not provided"})
		return
	}

	aoID, err := uuid.Parse(input.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auto optimize id"})
		return
	}

	status := model.AutoOptimizeStatus(input.Status)
	if status != model.AutoOptimizeStatusActive && status != model.AutoOptimizeStatusDisabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status provided"})
		return
	}

	err = s.optimizerService.ChangeStatus(c.Request.Context(), sc, accUUID, aoID, status)
	if err != nil {
		s.logger.Error("failed to change status", "error", err)
		handleServiceError(c, err, "failed to change status")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) autoOptimizerTrigger(c *gin.Context) {
	sc, args, accountID, processRequest := s.getHasuraRequestDetails(c)
	if !processRequest {
		return
	}
	// Validate accountID format
	if _, err := uuid.Parse(accountID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized to trigger auto optimize"})
		return
	}

	var input struct {
		ID string `json:"id" mapstructure:"id"`
	}

	if err := common.DecodeMapToStruct(args, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input format: " + err.Error()})
		return
	}

	if input.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	aoID, err := uuid.Parse(input.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auto optimize id"})
		return
	}

	err = s.optimizerService.ExecuteAutoOptimize(c.Request.Context(), aoID)
	if err != nil {
		s.logger.Error("failed to trigger auto optimize", "error", err)
		handleServiceError(c, err, "failed to trigger auto optimize")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "auto optimize triggered successfully"})
}

// Register these routes in server.go
func (s *Server) setupOptimizerRoutes() {
	// Group /autopilot
	ap := s.router.Group("/autopilot")
	ap.Use(optimizationEnabledMiddleware())
	{
		ap.POST("/recommendation", s.getAutoOptimizerRecommendation)
		ap.POST("/auto-optimize/workload", s.getAutoOptimizerWorkload)
		ap.POST("/execution/skip", s.autoPilotExecutionSkip)
		ap.POST("/update", s.autoOptimizerUpdate)
		ap.POST("", s.autoOptimizerCreate)
		ap.POST("/status", s.autoOptimizerChangeStatus)
		ap.POST("/trigger", s.autoOptimizerTrigger)
	}
}

// Need to add optimizerService to Server struct in server.go
func (s *Server) SetOptimizerService(svc optimizer.Service) {
	s.optimizerService = svc
	s.setupOptimizerRoutes()
}
