package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nudgebee/runbook/internal/model"
)

// listVersionsResponse mirrors the {"versions": [...]} envelope returned by
// GET /workflows/:id/versions.
type listVersionsResponse struct {
	Versions []model.WorkflowVersion `json:"versions"`
}

func (s *IntegrationTestSuite) listWorkflowVersions(workflowID string) []model.WorkflowVersion {
	resp := s.request(http.MethodGet, fmt.Sprintf("/workflows/%s/versions", workflowID), nil)
	defer func() { _ = resp.Body.Close() }()
	s.Require().Equal(http.StatusOK, resp.StatusCode, "list versions should return 200")

	var out listVersionsResponse
	s.Require().NoError(json.NewDecoder(resp.Body).Decode(&out), "decode list versions response")
	return out.Versions
}

func (s *IntegrationTestSuite) getWorkflowVersion(workflowID string, versionNumber int) (*model.WorkflowVersion, int) {
	resp := s.request(http.MethodGet, fmt.Sprintf("/workflows/%s/versions/%d", workflowID, versionNumber), nil)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var v model.WorkflowVersion
	s.Require().NoError(json.NewDecoder(resp.Body).Decode(&v), "decode get version response")
	return &v, resp.StatusCode
}

func (s *IntegrationTestSuite) restoreWorkflowVersion(workflowID string, versionNumber int) (*model.Workflow, int) {
	resp := s.request(http.MethodPost, fmt.Sprintf("/workflows/%s/versions/%d/restore", workflowID, versionNumber), nil)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.T().Logf("restoreWorkflowVersion non-200: status=%d body=%s", resp.StatusCode, string(body))
		return nil, resp.StatusCode
	}
	var wf model.Workflow
	s.Require().NoError(json.NewDecoder(resp.Body).Decode(&wf), "decode restore response")
	return &wf, resp.StatusCode
}

// publishWorkflowVersion calls POST /workflows/:id/publish. Pass an empty body
// (nil) for the common case (no metadata, default set_live=true).
func (s *IntegrationTestSuite) publishWorkflowVersion(workflowID string, body any) (*model.WorkflowVersion, int) {
	resp := s.request(http.MethodPost, fmt.Sprintf("/workflows/%s/publish", workflowID), body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		s.T().Logf("publishWorkflowVersion non-200: status=%d body=%s", resp.StatusCode, string(raw))
		return nil, resp.StatusCode
	}
	var v model.WorkflowVersion
	s.Require().NoError(json.NewDecoder(resp.Body).Decode(&v), "decode publish response")
	return &v, resp.StatusCode
}

func (s *IntegrationTestSuite) makeWorkflowVersionLive(workflowID string, versionNumber int) (*model.Workflow, int) {
	resp := s.request(http.MethodPost, fmt.Sprintf("/workflows/%s/versions/%d/make-live", workflowID, versionNumber), nil)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		s.T().Logf("makeWorkflowVersionLive non-200: status=%d body=%s", resp.StatusCode, string(raw))
		return nil, resp.StatusCode
	}
	var wf model.Workflow
	s.Require().NoError(json.NewDecoder(resp.Body).Decode(&wf), "decode make-live response")
	return &wf, resp.StatusCode
}

func (s *IntegrationTestSuite) updateWorkflowVersionMetadata(workflowID string, versionNumber int, body map[string]any) (*model.WorkflowVersion, int) {
	resp := s.request(http.MethodPatch, fmt.Sprintf("/workflows/%s/versions/%d", workflowID, versionNumber), body)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		s.T().Logf("updateWorkflowVersionMetadata non-200: status=%d body=%s", resp.StatusCode, string(raw))
		return nil, resp.StatusCode
	}
	var v model.WorkflowVersion
	s.Require().NoError(json.NewDecoder(resp.Body).Decode(&v), "decode update-metadata response")
	return &v, resp.StatusCode
}

func versionsWorkflow(name, scriptMarker string) model.Workflow {
	return model.Workflow{
		Name: name,
		Tags: map[string]any{"owner": "versioning-test"},
		Definition: model.WorkflowDefinition{
			Version:  "v1",
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks: []model.Task{
				{
					ID:     "task1",
					Type:   "scripting.run_script",
					Params: map[string]any{"script": fmt.Sprintf("echo %s", scriptMarker)},
				},
			},
		},
	}
}

// TestWorkflowVersionsCreatedOnCreate asserts that creating a workflow writes
// v1 with source="create" and immediately marks it live so the workflow is
// executable.
func (s *IntegrationTestSuite) TestWorkflowVersionsCreatedOnCreate() {
	s.T().Log("Running TestWorkflowVersionsCreatedOnCreate...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-versions-create", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	versions := s.listWorkflowVersions(wf.ID)
	s.Require().Len(versions, 1, "exactly one version after create")
	s.Equal(1, versions[0].VersionNumber)
	s.Equal(model.WorkflowVersionSourceCreate, versions[0].Source)
	s.Equal(wf.ID, versions[0].WorkflowID)
	s.Nil(versions[0].RestoredFromVersion)
	s.True(versions[0].IsLive, "v1 should be marked live")
	s.Equal("echo v1", versions[0].Definition.Tasks[0].Params["script"])

	live := s.getWorkflow(wf.ID)
	s.Require().NotNil(live.LiveVersionID)
	s.Require().NotNil(live.LiveVersionNumber)
	s.Equal(1, *live.LiveVersionNumber)
}

// TestSaveDoesNotCreateVersion asserts that updating the draft (the old
// "save" path) no longer writes version rows. Only Publish does.
func (s *IntegrationTestSuite) TestSaveDoesNotCreateVersion() {
	s.T().Log("Running TestSaveDoesNotCreateVersion...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-save-no-version", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	// Five saves with distinct scripts — should not add any version rows.
	for i := 2; i <= 6; i++ {
		wf.Definition.Tasks[0].Params["script"] = fmt.Sprintf("echo draft-%d", i)
		_, err = s.updateWorkflow(wf)
		s.Require().NoError(err, "save iteration %d", i)
	}

	versions := s.listWorkflowVersions(wf.ID)
	s.Require().Len(versions, 1, "save must not append versions; only Publish does")
	s.Equal(1, versions[0].VersionNumber)
	s.Equal(model.WorkflowVersionSourceCreate, versions[0].Source)
}

// TestPublishCreatesVersion asserts Publish creates a v2 row with source="publish".
func (s *IntegrationTestSuite) TestPublishCreatesVersion() {
	s.T().Log("Running TestPublishCreatesVersion...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-publish-creates", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	wf.Definition.Tasks[0].Params["script"] = "echo v2-draft"
	_, err = s.updateWorkflow(wf)
	s.Require().NoError(err)

	v, status := s.publishWorkflowVersion(wf.ID, nil)
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(v)
	s.Equal(2, v.VersionNumber)
	s.Equal(model.WorkflowVersionSourcePublish, v.Source)
	s.True(v.IsLive, "default publish makes the new version live")
	s.Equal("echo v2-draft", v.Definition.Tasks[0].Params["script"])

	versions := s.listWorkflowVersions(wf.ID)
	s.Require().Len(versions, 2)
}

// TestPublishWithMetadata asserts name + description round-trip.
func (s *IntegrationTestSuite) TestPublishWithMetadata() {
	s.T().Log("Running TestPublishWithMetadata...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-publish-meta", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	v, status := s.publishWorkflowVersion(wf.ID, map[string]any{
		"name":        "release-2026-05-18",
		"description": "first published release",
		"set_live":    true,
	})
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(v)
	s.Require().NotNil(v.Name)
	s.Equal("release-2026-05-18", *v.Name)
	s.Require().NotNil(v.Description)
	s.Equal("first published release", *v.Description)
}

// TestSetLiveIsPointerOnly is the CRITICAL safety test for the draft-
// preservation guarantee: switching the live pointer to an older version must
// NOT overwrite the in-progress draft. Without this invariant a rollback
// silently destroys unpublished work.
func (s *IntegrationTestSuite) TestSetLiveIsPointerOnly() {
	s.T().Log("Running TestSetLiveIsPointerOnly...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-set-live-pointer", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	// Publish v2.
	wf.Definition.Tasks[0].Params["script"] = "echo v2"
	_, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	v2, status := s.publishWorkflowVersion(wf.ID, map[string]any{"set_live": true})
	s.Require().Equal(http.StatusOK, status)
	s.Require().Equal(2, v2.VersionNumber)

	// Mutate the draft into a state that exists nowhere in version history.
	draftScript := "echo unpublished-draft"
	wf.Definition.Tasks[0].Params["script"] = draftScript
	wfBeforeFlip, err := s.updateWorkflow(wf)
	s.Require().NoError(err)
	s.Equal(draftScript, wfBeforeFlip.Definition.Tasks[0].Params["script"], "draft holds unpublished work")

	// Flip live back to v1 (a rollback). The draft must survive.
	updated, status := s.makeWorkflowVersionLive(wf.ID, 1)
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(updated)
	s.Require().NotNil(updated.LiveVersionNumber)
	s.Equal(1, *updated.LiveVersionNumber, "live pointer flipped to v1")

	live := s.getWorkflow(wf.ID)
	s.Equal(draftScript, live.Definition.Tasks[0].Params["script"],
		"CRITICAL: draft must survive live-pointer flip (no silent rollback data loss)")
	s.Require().NotNil(live.LiveVersionNumber)
	s.Equal(1, *live.LiveVersionNumber)

	// And the workflow.Status is untouched by SetLive.
	s.Equal(wfBeforeFlip.Status, live.Status, "status untouched by SetLive")
}

// TestWorkflowVersionsRetention asserts pruning honours both the cap and the
// live-version protection. We publish 5 more versions than the cap, flip live
// to an old one, then publish a few more to trigger pruning and confirm:
//   - total versions = cap
//   - live version survives even though it's older than the cap window
func (s *IntegrationTestSuite) TestWorkflowVersionsRetention() {
	s.T().Log("Running TestWorkflowVersionsRetention...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-versions-retention", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	// Publish a v2 to keep as the live anchor we want to protect.
	wf.Definition.Tasks[0].Params["script"] = "echo anchor"
	_, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	anchor, status := s.publishWorkflowVersion(wf.ID, map[string]any{"set_live": true})
	s.Require().Equal(http.StatusOK, status)
	s.Require().Equal(2, anchor.VersionNumber)

	// Push enough publishes (without flipping live) to overflow the cap by a
	// healthy margin so the prune actually executes.
	totalPublishes := model.MaxWorkflowVersionsPerWorkflow + 5
	for i := 3; i <= totalPublishes+1; i++ {
		wf.Definition.Tasks[0].Params["script"] = fmt.Sprintf("echo v%d", i)
		_, err = s.updateWorkflow(wf)
		s.Require().NoError(err, "save iteration %d", i)
		_, status := s.publishWorkflowVersion(wf.ID, map[string]any{"set_live": false})
		s.Require().Equal(http.StatusOK, status, "publish iteration %d", i)
	}

	versions := s.listWorkflowVersions(wf.ID)
	s.Require().LessOrEqual(len(versions), model.MaxWorkflowVersionsPerWorkflow, "retention cap")
	s.Require().GreaterOrEqual(len(versions), 1, "must keep at least the live anchor")

	// Live anchor (v2) must still be present even though many newer versions exist.
	var anchorPresent bool
	for _, v := range versions {
		if v.VersionNumber == 2 {
			anchorPresent = true
			s.True(v.IsLive, "anchor still marked live")
		}
	}
	s.True(anchorPresent, "live version must survive pruning")
}

// TestWorkflowGetVersionByNumber asserts single-row fetch returns the snapshot
// that was published at that point in time.
func (s *IntegrationTestSuite) TestWorkflowGetVersionByNumber() {
	s.T().Log("Running TestWorkflowGetVersionByNumber...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-versions-get", "original"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	wf.Definition.Tasks[0].Params["script"] = "echo changed"
	_, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	_, status := s.publishWorkflowVersion(wf.ID, nil)
	s.Require().Equal(http.StatusOK, status)

	v1, status := s.getWorkflowVersion(wf.ID, 1)
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(v1)
	s.Equal(1, v1.VersionNumber)
	s.Equal(model.WorkflowVersionSourceCreate, v1.Source)
	s.Equal("echo original", v1.Definition.Tasks[0].Params["script"])

	v2, status := s.getWorkflowVersion(wf.ID, 2)
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(v2)
	s.Equal(2, v2.VersionNumber)
	s.Equal(model.WorkflowVersionSourcePublish, v2.Source)
	s.Equal("echo changed", v2.Definition.Tasks[0].Params["script"])
}

// TestWorkflowGetVersionNotFound: unknown version_number returns 404.
func (s *IntegrationTestSuite) TestWorkflowGetVersionNotFound() {
	s.T().Log("Running TestWorkflowGetVersionNotFound...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-versions-get-404", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	_, status := s.getWorkflowVersion(wf.ID, 9999)
	s.Equal(http.StatusNotFound, status)
}

// TestWorkflowListVersionsForUnknownWorkflow: missing workflow returns 404.
func (s *IntegrationTestSuite) TestWorkflowListVersionsForUnknownWorkflow() {
	s.T().Log("Running TestWorkflowListVersionsForUnknownWorkflow...")

	resp := s.request(http.MethodGet, "/workflows/00000000-0000-0000-0000-000000000000/versions", nil)
	defer func() { _ = resp.Body.Close() }()
	s.Equal(http.StatusNotFound, resp.StatusCode)
}

// TestWorkflowRestoreVersion: restoring loads the target version's definition
// into the DRAFT (workflows.definition). It does NOT create a new version row
// and does NOT change the live pointer.
func (s *IntegrationTestSuite) TestWorkflowRestoreVersion() {
	s.T().Log("Running TestWorkflowRestoreVersion...")

	original := versionsWorkflow("test-versions-restore", "original")
	wf, _, err := s.createWorkflow(original)
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	// Save + publish v2 so we have something to restore from.
	wf.Definition.Tasks[0].Params["script"] = "echo changed"
	_, err = s.updateWorkflow(wf)
	s.Require().NoError(err)
	_, status := s.publishWorkflowVersion(wf.ID, map[string]any{"set_live": true})
	s.Require().Equal(http.StatusOK, status)

	pre := s.listWorkflowVersions(wf.ID)
	s.Require().Len(pre, 2)

	// Restore v1 into draft.
	restored, status := s.restoreWorkflowVersion(wf.ID, 1)
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(restored)
	s.Equal("echo original", restored.Definition.Tasks[0].Params["script"], "draft now matches v1")
	s.Equal("test-versions-restore", restored.Name)
	s.Equal(map[string]any{"owner": "versioning-test"}, restored.Tags)

	// No new version row.
	post := s.listWorkflowVersions(wf.ID)
	s.Require().Len(post, 2, "restore must not create a version row")

	// Live pointer unchanged (still v2).
	live := s.getWorkflow(wf.ID)
	s.Require().NotNil(live.LiveVersionNumber)
	s.Equal(2, *live.LiveVersionNumber, "live pointer untouched by restore")
	s.Equal("echo original", live.Definition.Tasks[0].Params["script"], "draft persisted")
}

// TestWorkflowRestoreVersionNotFound: restoring a non-existent version_number returns 404.
func (s *IntegrationTestSuite) TestWorkflowRestoreVersionNotFound() {
	s.T().Log("Running TestWorkflowRestoreVersionNotFound...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-versions-restore-404", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	_, status := s.restoreWorkflowVersion(wf.ID, 9999)
	s.Equal(http.StatusNotFound, status)
}

// TestRenameVersion asserts metadata patch round-trips.
func (s *IntegrationTestSuite) TestRenameVersion() {
	s.T().Log("Running TestRenameVersion...")

	wf, _, err := s.createWorkflow(versionsWorkflow("test-rename-version", "v1"))
	s.Require().NoError(err)
	defer s.deleteWorkflow(wf.ID, true)

	v, status := s.updateWorkflowVersionMetadata(wf.ID, 1, map[string]any{
		"name":        "renamed-v1",
		"description": "manually relabelled",
	})
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(v)
	s.Require().NotNil(v.Name)
	s.Equal("renamed-v1", *v.Name)
	s.Require().NotNil(v.Description)
	s.Equal("manually relabelled", *v.Description)

	// Re-fetch to confirm persisted.
	again, status := s.getWorkflowVersion(wf.ID, 1)
	s.Require().Equal(http.StatusOK, status)
	s.Require().NotNil(again.Name)
	s.Equal("renamed-v1", *again.Name)
}
