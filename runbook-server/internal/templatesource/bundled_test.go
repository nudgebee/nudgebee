package templatesource

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
)

const expectedBundledCount = 22

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestBundledSourceFetch(t *testing.T) {
	bundle, err := NewBundledSource().Fetch(context.Background())
	if err != nil {
		t.Fatalf("bundled fetch failed: %v", err)
	}
	if !bundle.OK {
		t.Fatal("bundle.OK is false")
	}
	if got := len(bundle.Templates); got != expectedBundledCount {
		t.Fatalf("expected %d bundled templates, got %d", expectedBundledCount, got)
	}

	seen := make(map[string]bool)
	for _, tmpl := range bundle.Templates {
		if tmpl.Slug == "" {
			t.Error("template with empty slug")
		}
		if seen[tmpl.Slug] {
			t.Errorf("duplicate slug %q", tmpl.Slug)
		}
		seen[tmpl.Slug] = true

		if tmpl.Name == "" {
			t.Errorf("%s: empty name", tmpl.Slug)
		}
		if tmpl.Status != "ACTIVE" {
			t.Errorf("%s: expected status ACTIVE, got %q", tmpl.Slug, tmpl.Status)
		}
		if !sha256Hex.MatchString(tmpl.Checksum) {
			t.Errorf("%s: checksum is not sha256 hex: %q", tmpl.Slug, tmpl.Checksum)
		}
		if !json.Valid([]byte(tmpl.DefinitionJSON)) {
			t.Errorf("%s: definition is not valid JSON", tmpl.Slug)
		}
		if !json.Valid([]byte(tmpl.TemplateVariablesJSON)) {
			t.Errorf("%s: template_variables is not valid JSON", tmpl.Slug)
		}
		if !json.Valid([]byte(tmpl.TagsJSON)) {
			t.Errorf("%s: tags is not valid JSON", tmpl.Slug)
		}
	}

	// Spot-check a couple of known slugs from each seed migration.
	for _, want := range []string{"rollout_restart_deployment", "investigate_rds_performance", "restart_cloud_sql_instance"} {
		if !seen[want] {
			t.Errorf("expected bundled template %q to be present", want)
		}
	}
}

func TestDecodeRejectsUnknownTaskType(t *testing.T) {
	yaml := []byte(`
slug: bogus
name: Bogus
category: kubernetes
status: ACTIVE
definition:
  version: v1
  triggers:
    - type: manual
  tasks:
    - id: do_evil
      type: definitely.not.a.real.task
      params: {}
`)
	if _, err := decode(yaml, validTaskTypes()); err == nil {
		t.Fatal("expected decode to reject an unknown task type, got nil error")
	}
}

func TestDecodeAcceptsKnownTaskType(t *testing.T) {
	yaml := []byte(`
slug: ok
name: OK
category: kubernetes
status: ACTIVE
definition:
  version: v1
  triggers:
    - type: manual
  tasks:
    - id: approve
      type: core.approval
      params:
        message: ok?
`)
	rt, err := decode(yaml, validTaskTypes())
	if err != nil {
		t.Fatalf("expected decode to accept core.approval, got %v", err)
	}
	if rt.Slug != "ok" {
		t.Fatalf("unexpected slug %q", rt.Slug)
	}
}
