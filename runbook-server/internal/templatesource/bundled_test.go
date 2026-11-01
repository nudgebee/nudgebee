package templatesource

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const expectedBundledCount = 23

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
	for _, want := range []string{"rollout_restart_deployment", "investigate_rds_performance", "restart_cloud_sql_instance", "application_diagnostic_check"} {
		if !seen[want] {
			t.Errorf("expected bundled template %q to be present", want)
		}
	}
}

// awsGlobalServices are AWS CLI services that are not region-scoped.
var awsGlobalServices = map[string]bool{"iam": true, "route53": true, "organizations": true, "sts": true, "cloudfront": true, "s3": true, "s3api": true}

// TestBundledAWSCommandsPassRegion guards against regional AWS CLI commands without
// --region: the collector then runs them in the cloud account's default region, so
// resources in any other region are reported as not found.
func TestBundledAWSCommandsPassRegion(t *testing.T) {
	bundle, err := NewBundledSource().Fetch(context.Background())
	if err != nil {
		t.Fatalf("bundled fetch failed: %v", err)
	}
	checked := 0
	for _, tmpl := range bundle.Templates {
		var def struct {
			Tasks []struct {
				ID     string         `json:"id"`
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(tmpl.DefinitionJSON), &def); err != nil {
			t.Fatalf("%s: definition is not valid JSON: %v", tmpl.Slug, err)
		}
		for _, task := range def.Tasks {
			if task.Type != "cloud.aws.cli" {
				continue
			}
			command, _ := task.Params["command"].(string)
			fields := strings.Fields(command)
			if len(fields) < 2 || fields[0] != "aws" || awsGlobalServices[fields[1]] {
				continue
			}
			checked++
			if !strings.Contains(command, "--region") {
				t.Errorf("%s/%s: regional AWS command has no --region: %q", tmpl.Slug, task.ID, command)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no cloud.aws.cli commands found; definition shape changed and this guard checks nothing")
	}
}

// TestBundledKubectlJSONArgsSurviveShell: the agent runs kubectl commands through
// /bin/sh, so an unquoted JSON patch loses its quotes and kubectl rejects it (#33449).
func TestBundledKubectlJSONArgsSurviveShell(t *testing.T) {
	bundle, err := NewBundledSource().Fetch(context.Background())
	if err != nil {
		t.Fatalf("bundled fetch failed: %v", err)
	}
	inputRef := regexp.MustCompile(`{{\s*Inputs\.\w+\s*}}`)
	checked := 0
	for _, tmpl := range bundle.Templates {
		var def struct {
			Tasks []struct {
				ID     string         `json:"id"`
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(tmpl.DefinitionJSON), &def); err != nil {
			t.Fatalf("%s: definition is not valid JSON: %v", tmpl.Slug, err)
		}
		for _, task := range def.Tasks {
			command, _ := task.Params["command"].(string)
			if task.Type != "cloud.k8s.cli" || !strings.Contains(command, "{\"") {
				continue
			}
			checked++
			rendered := inputRef.ReplaceAllString(command, "x1")
			// Print each argv word the shell would hand to kubectl, one per line.
			out, err := exec.Command("/bin/sh", "-c", `kubectl() { printf '%s\n' "$@"; }; `+rendered).Output()
			if err != nil {
				t.Fatalf("%s/%s: shell failed on %q: %v", tmpl.Slug, task.ID, rendered, err)
			}
			for _, arg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.HasPrefix(arg, "{") && !json.Valid([]byte(arg)) {
					t.Errorf("%s/%s: kubectl receives invalid JSON %q; quote it for the shell", tmpl.Slug, task.ID, arg)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no kubectl commands with a JSON argument found; this guard checks nothing")
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
