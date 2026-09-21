package api

import (
	"testing"

	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
)

func TestParseChangePlanBlock(t *testing.T) {
	t.Run("absent block returns nil", func(t *testing.T) {
		assert.Nil(t, parseChangePlanBlock("investigation text with no plan"))
	})

	t.Run("multi-line change value is captured in full", func(t *testing.T) {
		plan := parseChangePlanBlock("prose\n<change_plan>\nkind: source\nfiles: a/b.go, c/d.go\nchange: line one\nline two\nline three\n</change_plan>\ntrailing")
		if assert.NotNil(t, plan) {
			assert.Equal(t, "source", plan.Kind)
			assert.Equal(t, []string{"a/b.go", "c/d.go"}, plan.Files)
			assert.Equal(t, "line one\nline two\nline three", plan.Change)
		}
	})

	t.Run("capitalised keys are accepted", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nKind: deployment\nFiles: deploy/values.yaml\nChange: bump memory\nTarget_Value: 512Mi\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, "deployment", plan.Kind)
			assert.Equal(t, []string{"deploy/values.yaml"}, plan.Files)
			assert.Equal(t, "bump memory", plan.Change)
			assert.Equal(t, "512Mi", plan.TargetValue)
		}
	})

	t.Run("files split on newlines and commas", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: source\nfiles: a.go,\n b.go\n c.go\nchange: x\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, []string{"a.go", "b.go", "c.go"}, plan.Files)
		}
	})

	t.Run("files split on newlines and commas with list prefixes", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: source\nfiles: - a.go,\n * b.go\n c.go\nchange: x\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, []string{"a.go", "b.go", "c.go"}, plan.Files)
		}
	})

	t.Run("unknown kind defaults to source", func(t *testing.T) {
		plan := parseChangePlanBlock("<change_plan>\nkind: infra\nchange: x\n</change_plan>")
		if assert.NotNil(t, plan) {
			assert.Equal(t, "source", plan.Kind)
		}
	})

	t.Run("neither change nor files returns nil", func(t *testing.T) {
		assert.Nil(t, parseChangePlanBlock("<change_plan>\nkind: source\n</change_plan>"))
	})
}

func TestBuildFixModeQuery(t *testing.T) {
	t.Run("source plan is change plus located files", func(t *testing.T) {
		q := buildFixModeQuery(&codeChangePlan{
			Kind:   "source",
			Change: "trim the id",
			Files:  []string{"a/b.go", "c/d.go"},
		}, &services_server.EventCodeCapabilities{})
		assert.Equal(t, "trim the id\n\nFiles located during the investigation: a/b.go, c/d.go", q)
	})

	t.Run("deployment plan appends the values file and target", func(t *testing.T) {
		q := buildFixModeQuery(&codeChangePlan{
			Kind:        "deployment",
			Change:      "raise the limit",
			TargetValue: "512Mi",
		}, &services_server.EventCodeCapabilities{
			Deployment: &services_server.DeploymentCapability{
				ValuesPath: "deploy/values-dev.yaml",
			},
		})
		assert.Contains(t, q, "raise the limit")
		assert.Contains(t, q, "Edit the deployment values file deploy/values-dev.yaml to 512Mi")
	})
}
