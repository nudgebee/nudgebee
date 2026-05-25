package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

func TestParseVersionArgs(t *testing.T) {
	t.Run("missing account_id", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"id": "wf-1", "version_number": float64(1)})
		assert.Error(t, err)
	})
	t.Run("empty account_id", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"account_id": "", "id": "wf-1", "version_number": float64(1)})
		assert.Error(t, err)
	})
	t.Run("missing id", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"account_id": "acc", "version_number": float64(1)})
		assert.Error(t, err)
	})
	t.Run("missing version_number", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1"})
		assert.Error(t, err)
	})
	t.Run("zero version_number", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1", "version_number": float64(0)})
		assert.Error(t, err)
	})
	t.Run("negative version_number", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1", "version_number": float64(-1)})
		assert.Error(t, err)
	})
	t.Run("wrong type for version_number", func(t *testing.T) {
		_, _, _, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1", "version_number": "1"})
		assert.Error(t, err)
	})
	t.Run("float64 version_number", func(t *testing.T) {
		acc, id, ver, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1", "version_number": float64(7)})
		assert.NoError(t, err)
		assert.Equal(t, "acc", acc)
		assert.Equal(t, "wf-1", id)
		assert.Equal(t, 7, ver)
	})
	t.Run("int version_number", func(t *testing.T) {
		_, _, ver, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1", "version_number": 4})
		assert.NoError(t, err)
		assert.Equal(t, 4, ver)
	})
	t.Run("int64 version_number", func(t *testing.T) {
		_, _, ver, err := parseVersionArgs(map[string]any{"account_id": "acc", "id": "wf-1", "version_number": int64(9)})
		assert.NoError(t, err)
		assert.Equal(t, 9, ver)
	})
}

type ActionsVersionsHandlerTestSuite struct {
	APITestSuite
}

func (s *ActionsVersionsHandlerTestSuite) makeActionRequest(action string, args map[string]any) (*httptest.ResponseRecorder, *http.Request) {
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	s.securityContextBuilder.On("BuildContextFromPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sc, nil)

	actionReq := ActionRequest{
		Action: ActionRequestAction{Name: action},
		Input:  map[string]any{"request": args},
	}
	body, _ := json.Marshal(actionReq)
	req, _ := http.NewRequest(http.MethodPost, "/hasura", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	return w, req
}

func (s *ActionsVersionsHandlerTestSuite) TestListVersionsSuccess() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	expected := []model.WorkflowVersion{
		{ID: "v-1", WorkflowID: "wf-1", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate},
	}
	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-1").Return(expected, nil)

	w, req := s.makeActionRequest("workflow_list_versions", map[string]any{"account_id": "test-account", "id": "wf-1"})
	s.server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	versions, ok := resp["versions"].([]any)
	if assert.True(t, ok, "versions key should be array") {
		assert.Len(t, versions, 1)
	}
}

func (s *ActionsVersionsHandlerTestSuite) TestListVersionsMissingArgs() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	w, req := s.makeActionRequest("workflow_list_versions", map[string]any{"id": "wf-1"})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w, req = s.makeActionRequest("workflow_list_versions", map[string]any{"account_id": "test-account"})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestListVersionsNotFound() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-missing").Return(nil, sql.ErrNoRows)

	w, req := s.makeActionRequest("workflow_list_versions", map[string]any{"account_id": "test-account", "id": "wf-missing"})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestListVersionsEmptyReturnsArray() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-empty").Return(([]model.WorkflowVersion)(nil), nil)

	w, req := s.makeActionRequest("workflow_list_versions", map[string]any{"account_id": "test-account", "id": "wf-empty"})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	versions, ok := resp["versions"].([]any)
	if assert.True(t, ok, "versions should be array not null") {
		assert.Len(t, versions, 0)
	}
}

func (s *ActionsVersionsHandlerTestSuite) TestGetVersionSuccess() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	expected := &model.WorkflowVersion{ID: "v-1", WorkflowID: "wf-1", VersionNumber: 2, Source: model.WorkflowVersionSourceSave}
	s.workflowService.On("GetWorkflowVersion", mock.Anything, "test-account", "wf-1", 2).Return(expected, nil)

	w, req := s.makeActionRequest("workflow_get_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(2)})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestGetVersionInvalidArgs() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	w, req := s.makeActionRequest("workflow_get_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(0)})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestGetVersionNotFound() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	s.workflowService.On("GetWorkflowVersion", mock.Anything, "test-account", "wf-1", 99).Return((*model.WorkflowVersion)(nil), sql.ErrNoRows)

	w, req := s.makeActionRequest("workflow_get_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(99)})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestRestoreVersionSuccess() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	restored := model.Workflow{ID: "wf-1", Name: "wf-1"}
	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 3).Return(restored, nil)

	w, req := s.makeActionRequest("workflow_restore_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(3)})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestRestoreVersionNotFound() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 5).Return(model.Workflow{}, sql.ErrNoRows)

	w, req := s.makeActionRequest("workflow_restore_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(5)})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestRestoreVersionUnauthorized() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 2).Return(model.Workflow{}, common.ErrorUnauthorized("account not accessible"))

	w, req := s.makeActionRequest("workflow_restore_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(2)})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestRestoreVersionServiceError() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 2).Return(model.Workflow{}, errors.New("db kaboom"))

	w, req := s.makeActionRequest("workflow_restore_version", map[string]any{"account_id": "test-account", "id": "wf-1", "version_number": float64(2)})
	s.server.router.ServeHTTP(w, req)
	// handleServiceError clamps non-common.Error 5xx to 400 to avoid Hasura wrapping internal details.
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestPublishVersionAction() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	name := "rel-x"
	desc := "first"
	v := &model.WorkflowVersion{ID: "v-2", WorkflowID: "wf-1", VersionNumber: 2, Source: model.WorkflowVersionSourcePublish, Name: &name, Description: &desc, IsLive: true}
	s.workflowService.On("PublishWorkflow", mock.Anything, "test-account", "wf-1", &name, &desc, true).Return(v, nil)

	w, req := s.makeActionRequest("workflows_publish_version", map[string]any{
		"account_id":  "test-account",
		"id":          "wf-1",
		"name":        name,
		"description": desc,
		"set_live":    true,
	})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestPublishVersionActionDefaultsSetLive() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	v := &model.WorkflowVersion{ID: "v-2", WorkflowID: "wf-1", VersionNumber: 2, Source: model.WorkflowVersionSourcePublish, IsLive: true}
	s.workflowService.On("PublishWorkflow", mock.Anything, "test-account", "wf-1", (*string)(nil), (*string)(nil), true).Return(v, nil)

	w, req := s.makeActionRequest("workflows_publish_version", map[string]any{
		"account_id": "test-account",
		"id":         "wf-1",
	})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestMakeVersionLiveAction() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	num := 2
	wf := &model.Workflow{ID: "wf-1", LiveVersionNumber: &num}
	s.workflowService.On("SetLiveWorkflowVersion", mock.Anything, "test-account", "wf-1", 2).Return(wf, nil)

	w, req := s.makeActionRequest("workflows_make_version_live", map[string]any{
		"account_id":     "test-account",
		"id":             "wf-1",
		"version_number": float64(2),
	})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *ActionsVersionsHandlerTestSuite) TestUpdateVersionMetadataAction() {
	gin.SetMode(gin.TestMode)
	t := s.T()

	name := "renamed"
	v := &model.WorkflowVersion{ID: "v-1", WorkflowID: "wf-1", VersionNumber: 1, Name: &name}
	s.workflowService.On("UpdateWorkflowVersionMetadata", mock.Anything, "test-account", "wf-1", 1, &name, (*string)(nil)).Return(v, nil)

	w, req := s.makeActionRequest("workflows_update_version_metadata", map[string]any{
		"account_id":     "test-account",
		"id":             "wf-1",
		"version_number": float64(1),
		"name":           name,
	})
	s.server.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestActionsVersionsHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(ActionsVersionsHandlerTestSuite))
}
