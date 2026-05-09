package scan_orchestrator

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Real popeye v0.21.5 output skeleton (one section per resource type;
// `issues` map keyed by `namespace/name` for namespaced resources, `name`
// alone for cluster-scoped).
const popeyeFixture = `{
  "popeye": {
    "report_time": "2026-05-08T17:32:00Z",
    "score": 76,
    "grade": "C",
    "errors": [],
    "sections": [
      {
        "linter": "deployments",
        "gvr": "apps/v1/deployments",
        "tally": {"ok": 5, "info": 0, "warning": 3, "error": 0, "score": 80},
        "issues": {
          "default/my-app": [
            {"group": "__root__", "gvr": "apps/v1/deployments", "level": 0, "message": "[POP-1100] healthy"},
            {"group": "__root__", "gvr": "apps/v1/deployments", "level": 2, "message": "[POP-106] No resources defined"},
            {"group": "__root__", "gvr": "apps/v1/deployments", "level": 3, "message": "[POP-100] No probes defined"}
          ],
          "default/clean-app": [
            {"group": "__root__", "gvr": "apps/v1/deployments", "level": 0, "message": "[POP-1110] healthy"}
          ]
        }
      },
      {
        "linter": "pods",
        "gvr": "v1/pods",
        "tally": {"ok": 0, "info": 0, "warning": 1, "error": 0, "score": 0},
        "issues": {
          "kube-system/kube-proxy-abc": [
            {"group": "__root__", "gvr": "v1/pods", "level": 1, "message": "[POP-300] container should run as non-root user"}
          ]
        }
      },
      {
        "linter": "clusterroles",
        "gvr": "rbac.authorization.k8s.io/v1/clusterroles",
        "tally": {"ok": 10, "info": 0, "warning": 1, "error": 0, "score": 90},
        "issues": {
          "stale-cluster-role": [
            {"group": "__root__", "gvr": "rbac.authorization.k8s.io/v1/clusterroles", "level": 1, "message": "[POP-400] Used? Unable to locate resource reference"}
          ]
        }
      }
    ]
  }
}`

func TestParsePopeye_RealSchema(t *testing.T) {
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}
	recs, err := ParsePopeye(popeyeFixture, account)
	if err != nil {
		t.Fatalf("ParsePopeye: %v", err)
	}
	// Expected: my-app (Critical), kube-proxy-abc (Info), stale-cluster-role (Info).
	// clean-app dropped (only level=0).
	if len(recs) != 3 {
		t.Fatalf("recs = %d; want 3 (clean-app dropped)", len(recs))
	}

	// Map orders are randomized — index by AccountObjectID for stable assertions.
	byID := make(map[string]Recommendation, len(recs))
	for _, r := range recs {
		byID[r.AccountObjectID] = r
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].AccountObjectID < recs[j].AccountObjectID })

	myApp, ok := byID["default/Deployment/my-app"]
	if !ok {
		t.Fatalf("missing my-app row; have keys: %+v", recs)
	}
	if myApp.Severity != "Critical" {
		t.Errorf("my-app.Severity = %q; want Critical (max level 3)", myApp.Severity)
	}
	if myApp.RuleName != PopeyeRuleName {
		t.Errorf("my-app.RuleName = %q", myApp.RuleName)
	}
	var insights []map[string]any
	if err := json.Unmarshal([]byte(myApp.Recommendation), &insights); err != nil {
		t.Fatalf("recommendation not valid JSON: %v", err)
	}
	if len(insights) != 2 {
		t.Errorf("level==0 issue not dropped: %d insights", len(insights))
	}
	for _, ins := range insights {
		if msg := ins["message"].(string); strings.Contains(msg, "[POP-") {
			t.Errorf("popeye code not stripped from message: %q", msg)
		}
		if ins["namespace"] != "default" {
			t.Errorf("namespace not parsed; got %v", ins["namespace"])
		}
		if ins["kind"] != "Deployment" {
			t.Errorf("kind not normalized; got %v", ins["kind"])
		}
		if ins["gvr"] != "apps/v1/deployments" {
			t.Errorf("gvr not propagated; got %v", ins["gvr"])
		}
	}

	kp, ok := byID["kube-system/Pod/kube-proxy-abc"]
	if !ok {
		t.Fatalf("missing kube-proxy-abc row")
	}
	if kp.Severity != "Info" {
		t.Errorf("kube-proxy.Severity = %q; want Info (only level 1)", kp.Severity)
	}

	// cluster-scoped — namespace component stays empty, key starts with "/".
	cr, ok := byID["/ClusterRole/stale-cluster-role"]
	if !ok {
		t.Fatalf("missing stale-cluster-role row")
	}
	if cr.Severity != "Info" {
		t.Errorf("clusterrole.Severity = %q", cr.Severity)
	}
}

func TestParsePopeye_RejectsInvalidJSON(t *testing.T) {
	if _, err := ParsePopeye("not-json", ScanAccount{}); err == nil {
		t.Fatal("expected error on non-JSON input")
	}
}

func TestParsePopeye_DropsZeroLevelOnlyResources(t *testing.T) {
	stdout := `{"popeye":{"score":100,"sections":[{"linter":"pods","gvr":"v1/pods","issues":{"x/y":[{"group":"_","gvr":"v1/pods","level":0,"message":"all good"}]}}]}}`
	recs, err := ParsePopeye(stdout, ScanAccount{AccountID: "a", TenantID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Errorf("expected 0 recs (all level=0); got %d", len(recs))
	}
}

func TestPopeyeScore(t *testing.T) {
	score, err := PopeyeScore(popeyeFixture)
	if err != nil {
		t.Fatal(err)
	}
	if score != 76 {
		t.Errorf("score = %v; want 76", score)
	}
}

func TestSplitPopeyeCode(t *testing.T) {
	cases := []struct {
		in       string
		wantCode string
		wantMsg  string
	}{
		{"[POP-100] No probes defined", "100", "No probes defined"},
		{"[POP-300] container should run as non-root user", "300", "container should run as non-root user"},
		{"plain message", "", "plain message"},
		{"  [POP-42]  spaced  ", "42", "spaced"},
	}
	for _, c := range cases {
		gotCode, gotMsg := splitPopeyeCode(c.in)
		if gotCode != c.wantCode || gotMsg != c.wantMsg {
			t.Errorf("splitPopeyeCode(%q) = (%q, %q); want (%q, %q)", c.in, gotCode, gotMsg, c.wantCode, c.wantMsg)
		}
	}
}

func TestSplitResourceKey(t *testing.T) {
	cases := map[string]struct{ ns, name string }{
		"default/my-app":          {"default", "my-app"},
		"kube-system/kube-proxy":  {"kube-system", "kube-proxy"},
		"stale-cluster-role":      {"", "stale-cluster-role"},
		"some/namespace/with-slash": {"some", "namespace/with-slash"},
	}
	for in, want := range cases {
		ns, name := splitResourceKey(in)
		if ns != want.ns || name != want.name {
			t.Errorf("splitResourceKey(%q) = (%q, %q); want (%q, %q)", in, ns, name, want.ns, want.name)
		}
	}
}

func TestNormalizePopeyeKind(t *testing.T) {
	cases := map[string]string{
		"deployments":         "Deployment",
		"statefulsets":        "StatefulSet",
		"daemonsets":          "DaemonSet",
		"pods":                "Pod",
		"services":            "Service",
		"clusterroles":        "ClusterRole",
		"clusterrolebindings": "ClusterRoleBinding",
		"some-unknown-kind":   "some-unknown-kind", // unchanged
	}
	for in, want := range cases {
		if got := normalizePopeyeKind(in); got != want {
			t.Errorf("normalizePopeyeKind(%q) = %q; want %q", in, got, want)
		}
	}
}
