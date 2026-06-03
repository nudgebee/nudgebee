package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type VersionsRESTHandlerTestSuite struct {
	APITestSuite
}

func (s *VersionsRESTHandlerTestSuite) doRequest(method, path string) *httptest.ResponseRecorder {
	return s.doRequestWithBody(method, path, nil)
}

func (s *VersionsRESTHandlerTestSuite) doRequestWithBody(method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, path, reader)
	req.Header.Set("X-Tenant-ID", "test-tenant")
	req.Header.Set("X-Account-ID", "test-account")
	req.Header.Set("X-User-ID", "test-user")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.server.router.ServeHTTP(w, req)
	return w
}

func (s *VersionsRESTHandlerTestSuite) TestListVersionsSuccess() {
	t := s.T()
	expected := []model.WorkflowVersion{
		{ID: "v-1", WorkflowID: "wf-1", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate},
		{ID: "v-2", WorkflowID: "wf-1", VersionNumber: 2, Source: model.WorkflowVersionSourcePublish},
	}
	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-1").Return(expected, nil)

	w := s.doRequest(http.MethodGet, "/workflows/wf-1/versions")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	versions, ok := resp["versions"].([]any)
	if assert.True(t, ok) {
		assert.Len(t, versions, 2)
	}
}

func (s *VersionsRESTHandlerTestSuite) TestListVersionsNotFound() {
	t := s.T()
	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-missing").Return(nil, sql.ErrNoRows)

	w := s.doRequest(http.MethodGet, "/workflows/wf-missing/versions")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestListVersionsCommonErrorPropagatesCode() {
	t := s.T()
	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-forbid").Return(nil, common.ErrorUnauthorized("nope"))

	w := s.doRequest(http.MethodGet, "/workflows/wf-forbid/versions")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestListVersionsInternalError() {
	t := s.T()
	s.workflowService.On("ListWorkflowVersions", mock.Anything, "test-account", "wf-boom").Return(nil, errors.New("boom"))

	w := s.doRequest(http.MethodGet, "/workflows/wf-boom/versions")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestGetVersionSuccess() {
	t := s.T()
	expected := &model.WorkflowVersion{ID: "v-2", WorkflowID: "wf-1", VersionNumber: 2, Source: model.WorkflowVersionSourcePublish}
	s.workflowService.On("GetWorkflowVersion", mock.Anything, "test-account", "wf-1", 2).Return(expected, nil)

	w := s.doRequest(http.MethodGet, "/workflows/wf-1/versions/2")
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestGetVersionInvalidNumber() {
	t := s.T()
	w := s.doRequest(http.MethodGet, "/workflows/wf-1/versions/abc")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = s.doRequest(http.MethodGet, "/workflows/wf-1/versions/0")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = s.doRequest(http.MethodGet, "/workflows/wf-1/versions/-5")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestGetVersionNotFound() {
	t := s.T()
	s.workflowService.On("GetWorkflowVersion", mock.Anything, "test-account", "wf-1", 99).Return((*model.WorkflowVersion)(nil), sql.ErrNoRows)

	w := s.doRequest(http.MethodGet, "/workflows/wf-1/versions/99")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestRestoreVersionSuccess() {
	t := s.T()
	restored := model.Workflow{ID: "wf-1", Name: "wf-1", Status: model.WorkflowStatusActive}
	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 3).Return(restored, nil)

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/3/restore")
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestRestoreVersionInvalidNumber() {
	t := s.T()
	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/abc/restore")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestRestoreVersionNotFound() {
	t := s.T()
	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 99).Return(model.Workflow{}, sql.ErrNoRows)

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/99/restore")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestRestoreVersionUnauthorized() {
	t := s.T()
	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 1).Return(model.Workflow{}, common.ErrorUnauthorized("not allowed"))

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/1/restore")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestRestoreVersionInternalError() {
	t := s.T()
	s.workflowService.On("RestoreWorkflowVersion", mock.Anything, "test-account", "wf-1", 1).Return(model.Workflow{}, errors.New("db kaboom"))

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/1/restore")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestPublishVersionSuccessNoBody() {
	t := s.T()
	v := &model.WorkflowVersion{ID: "v-2", WorkflowID: "wf-1", VersionNumber: 2, Source: model.WorkflowVersionSourcePublish, IsLive: true}
	s.workflowService.On("PublishWorkflow", mock.Anything, "test-account", "wf-1", (*string)(nil), (*string)(nil), true).Return(v, nil)

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/publish")
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestPublishVersionWithMetadata() {
	t := s.T()
	name := "release-x"
	desc := "first release"
	v := &model.WorkflowVersion{ID: "v-3", WorkflowID: "wf-1", VersionNumber: 3, Source: model.WorkflowVersionSourcePublish, Name: &name, Description: &desc, IsLive: false}
	s.workflowService.On("PublishWorkflow", mock.Anything, "test-account", "wf-1", &name, &desc, false).Return(v, nil)

	w := s.doRequestWithBody(http.MethodPost, "/workflows/wf-1/publish", map[string]any{
		"name":        name,
		"description": desc,
		"set_live":    false,
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestMakeVersionLiveSuccess() {
	t := s.T()
	num := 2
	wf := &model.Workflow{ID: "wf-1", Name: "wf-1", LiveVersionNumber: &num}
	s.workflowService.On("SetLiveWorkflowVersion", mock.Anything, "test-account", "wf-1", 2).Return(wf, nil)

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/2/make-live")
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestMakeVersionLiveNotFound() {
	t := s.T()
	s.workflowService.On("SetLiveWorkflowVersion", mock.Anything, "test-account", "wf-1", 99).Return((*model.Workflow)(nil), sql.ErrNoRows)

	w := s.doRequest(http.MethodPost, "/workflows/wf-1/versions/99/make-live")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestUpdateVersionMetadataSuccess() {
	t := s.T()
	name := "rename"
	v := &model.WorkflowVersion{ID: "v-1", WorkflowID: "wf-1", VersionNumber: 1, Name: &name}
	s.workflowService.On("UpdateWorkflowVersionMetadata", mock.Anything, "test-account", "wf-1", 1, &name, (*string)(nil)).Return(v, nil)

	w := s.doRequestWithBody(http.MethodPatch, "/workflows/wf-1/versions/1", map[string]any{"name": name})
	assert.Equal(t, http.StatusOK, w.Code)
}

func (s *VersionsRESTHandlerTestSuite) TestUpdateVersionMetadataInvalidNumber() {
	t := s.T()
	w := s.doRequestWithBody(http.MethodPatch, "/workflows/wf-1/versions/0", map[string]any{"name": "x"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestVersionsRESTHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(VersionsRESTHandlerTestSuite))
}
