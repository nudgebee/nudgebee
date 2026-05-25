package scan_orchestrator

import (
	"encoding/json"
	"sort"
	"testing"
)

// kubeBenchFixture mirrors the real shape captured from a `kube-bench --json`
// run on a worker node. Top-level Controls[]; each Control's tests[]; each
// test's results[] is what we emit one row per.
const kubeBenchFixture = `{
  "Controls": [
    {
      "id": "3",
      "version": "cis-1.23",
      "text": "Worker Node Security Configuration",
      "node_type": "node",
      "tests": [
        {
          "section": "3.1",
          "type": "manual",
          "desc": "Worker Node Configuration Files",
          "results": [
            {
              "test_number": "3.1.1",
              "test_desc": "Ensure that the proxy kubeconfig file permissions are set to 644 or more restrictive (Manual)",
              "audit": "/bin/sh -c 'if test -e $proxykubeconfig; then stat -c permissions=%a $proxykubeconfig; fi'",
              "type": "manual",
              "remediation": "...",
              "test_info": ["..."],
              "status": "WARN",
              "actual_value": "permissions=644",
              "scored": false,
              "expected_result": "permissions has permissions 644, expected 644 or more restrictive"
            },
            {
              "test_number": "3.1.2",
              "test_desc": "Ensure that the proxy kubeconfig file ownership is set to root:root (Manual)",
              "type": "manual",
              "status": "PASS"
            }
          ]
        },
        {
          "section": "3.2",
          "type": "automated",
          "desc": "Kubelet",
          "results": [
            {
              "test_number": "3.2.1",
              "test_desc": "Ensure that the --anonymous-auth argument is set to false (Automated)",
              "status": "FAIL",
              "remediation": "Edit kubelet config..."
            }
          ]
        }
      ]
    }
  ]
}`

func TestParseKubeBench_RealSchema(t *testing.T) {
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}
	recs, err := ParseKubeBench(kubeBenchFixture, account)
	if err != nil {
		t.Fatalf("ParseKubeBench: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("recs = %d; want 3 (one per result, all statuses included)", len(recs))
	}

	sort.Slice(recs, func(i, j int) bool { return recs[i].AccountObjectID < recs[j].AccountObjectID })

	wantIDs := []string{"3.1.1", "3.1.2", "3.2.1"}
	for i, w := range wantIDs {
		if recs[i].AccountObjectID != w {
			t.Errorf("rec[%d].AccountObjectID = %q; want %q", i, recs[i].AccountObjectID, w)
		}
		if recs[i].RuleName != KubeBenchRuleName {
			t.Errorf("rec[%d].RuleName = %q; want %q (uppercase CIS)", i, recs[i].RuleName, KubeBenchRuleName)
		}
		if recs[i].Category != "Security" {
			t.Errorf("rec[%d].Category = %q", i, recs[i].Category)
		}
		// Collector hardcodes Info for every kube_bench row — keeping parity.
		if recs[i].Severity != "Info" {
			t.Errorf("rec[%d].Severity = %q; want Info (matches collector behaviour)", i, recs[i].Severity)
		}
	}

	// Sample row's recommendation JSON must include the original result fields
	// so the UI surface has audit, remediation, status, expected_result, etc.
	var first map[string]any
	if err := json.Unmarshal([]byte(recs[0].Recommendation), &first); err != nil {
		t.Fatalf("recommendation[0] not valid JSON: %v", err)
	}
	if first["status"] != "WARN" {
		t.Errorf("recommendation[0].status = %v; want WARN", first["status"])
	}
	if first["test_desc"] == nil {
		t.Error("recommendation[0].test_desc missing")
	}
}

func TestParseKubeBench_StripsPreamble(t *testing.T) {
	stdout := "kube-bench: warning: configdir not found, falling back\n" + kubeBenchFixture
	recs, err := ParseKubeBench(stdout, ScanAccount{AccountID: "a", TenantID: "t"})
	if err != nil {
		t.Fatalf("ParseKubeBench with preamble: %v", err)
	}
	if len(recs) != 3 {
		t.Errorf("recs = %d; want 3", len(recs))
	}
}

func TestParseKubeBench_SkipsRowsMissingTestNumber(t *testing.T) {
	stdout := `{"Controls":[{"tests":[{"results":[{"test_desc":"orphan, no number"},{"test_number":"x.y","status":"PASS"}]}]}]}`
	recs, err := ParseKubeBench(stdout, ScanAccount{AccountID: "a", TenantID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("recs = %d; want 1 (orphan dropped)", len(recs))
	}
}

func TestParseKubeBench_RejectsNonJSON(t *testing.T) {
	if _, err := ParseKubeBench("only logs no json", ScanAccount{}); err == nil {
		t.Fatal("expected error on non-JSON input")
	}
}

func TestTruncateUTF8Safe(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"shorter than max", "hello", 100, "hello"},
		{"ascii cut at byte boundary", "hello world", 5, "hello"},
		{
			// multi-byte UTF-8: '世' = E4 B8 96, '界' = E7 95 8C
			// max=4 lands inside '界' — should back up to keep just '世'
			name: "multi-byte rune straddles cut",
			in:   "a世界",
			max:  4,
			want: "a世",
		},
		{
			// max=1 cuts mid-'世' — back up to empty (a is 1 byte but max=1
			// keeps it; verify exact behaviour at boundary)
			name: "cut right after 1-byte char + multi-byte starts",
			in:   "a世",
			max:  1,
			want: "a",
		},
		{
			// 4-byte rune (emoji 🦀 = F0 9F A6 80). max=2 cuts mid-emoji.
			name: "4-byte rune backed up to start",
			in:   "x🦀y",
			max:  3,
			want: "x",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateUTF8Safe(c.in, c.max)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
