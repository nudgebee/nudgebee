package api

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	"nudgebee/llm/common"
	"nudgebee/llm/knowledge"
	"nudgebee/llm/security"
)

// Skill sources (#41513 / #41514): organisation-wide, tenant admins only. The
// service re-checks the caller's tenant on every call.

type kbSkillSourceRequest struct {
	Id         string   `json:"id"`
	Repo       string   `json:"repo"`
	Folder     string   `json:"folder"`
	Branch     string   `json:"branch"`
	Visibility string   `json:"visibility"`
	AccountIds []string `json:"account_ids"`
}

func kbSkillSourceDecode(c *gin.Context, context *security.RequestContext, payload map[string]any, needId bool) (kbSkillSourceRequest, bool) {
	var request kbSkillSourceRequest
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return request, false
	}
	if needId && request.Id == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("kb: id is required")}))
		return request, false
	}
	if !orgKBWriter(context) {
		c.JSON(403, BuildApiResponse(nil, []error{common.Error{Message: errorKBUserAccessMessage}}))
		return request, false
	}
	return request, true
}

func kbCreateSkillSource(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request, ok := kbSkillSourceDecode(c, context, payload, false)
	if !ok {
		return
	}
	resp, err := knowledge.CreateSkillSource(context, knowledge.SkillSource{
		Repo: request.Repo, Folder: request.Folder, Branch: request.Branch,
		Visibility: request.Visibility, AccountIds: request.AccountIds,
	})
	if err != nil {
		slog.Warn("kb: failed to create skill source", "error", err, "repo", request.Repo)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, BuildApiResponse(resp, nil))
}

func kbListSkillSources(c *gin.Context, context *security.RequestContext, _ map[string]any) {
	if !context.GetSecurityContext().HasTenantAccess(security.SecurityAccessTypeRead) {
		c.JSON(403, BuildApiResponse(nil, []error{common.Error{Message: errorKBUserAccessMessage}}))
		return
	}
	resp, err := knowledge.ListSkillSources(context)
	if err != nil {
		slog.Error("kb: failed to list skill sources", "error", err)
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, BuildApiResponse(resp, nil))
}

func kbUpdateSkillSourceVisibility(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request, ok := kbSkillSourceDecode(c, context, payload, true)
	if !ok {
		return
	}
	resp, err := knowledge.UpdateSkillSourceVisibility(context, request.Id, request.Visibility, request.AccountIds)
	if err != nil {
		slog.Warn("kb: failed to update skill source", "error", err, "source_id", request.Id)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, BuildApiResponse(resp, nil))
}

func kbSyncSkillSource(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request, ok := kbSkillSourceDecode(c, context, payload, true)
	if !ok {
		return
	}
	report, err := knowledge.SyncSkillSource(context, request.Id)
	if err != nil {
		slog.Warn("kb: skill source sync failed", "error", err, "source_id", request.Id)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, BuildApiResponse(report, nil))
}

func kbDeleteSkillSource(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request, ok := kbSkillSourceDecode(c, context, payload, true)
	if !ok {
		return
	}
	if err := knowledge.DeleteSkillSource(context, request.Id); err != nil {
		slog.Warn("kb: failed to delete skill source", "error", err, "source_id", request.Id)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, BuildApiResponse(map[string]any{"status": "ok", "id": request.Id}, nil))
}
