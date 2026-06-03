package scan_orchestrator

import (
	"testing"
	"time"
)

// jobEntry builds a minimal schedule_jobs wire-shape entry for tests.
func jobEntry(actionFunc string, lastExecSec int64) map[string]any {
	return map[string]any{
		"runnable_params": map[string]any{"action_func_name": actionFunc},
		"state":           map[string]any{"last_exec_time_sec": float64(lastExecSec)},
	}
}

func TestMissedScanners_NoPriorState_AllMissed(t *testing.T) {
	got := missedScanners(nil, time.Now())
	want := scheduledScanners()
	if len(got) != len(want) {
		t.Fatalf("expected %d missed (all scanners), got %d: %v", len(want), len(got), got)
	}
}

func TestMissedScanners_FreshState_NoneMissed(t *testing.T) {
	// last_exec = now, so for every scanner cron the next tick is in the
	// future relative to now — none should be missed.
	now := time.Now()
	entries := make([]any, 0)
	for _, name := range scheduledScanners() {
		entries = append(entries, jobEntry(name, now.Unix()))
	}
	got := missedScanners(entries, now)
	if len(got) != 0 {
		t.Fatalf("expected no missed scanners with fresh state, got %v", got)
	}
}

func TestMissedScanners_StaleWeeklyScannerOnly(t *testing.T) {
	// popeye_scan cron is "0 12 * * 1" (Mon 12:00). last_exec = 10 days ago
	// → next tick (Monday after that) is already in the past → missed.
	// All others get fresh state → not missed.
	now := time.Now()
	stale := now.Add(-10 * 24 * time.Hour).Unix()
	entries := make([]any, 0)
	for _, name := range scheduledScanners() {
		if name == "popeye_scan" {
			entries = append(entries, jobEntry(name, stale))
		} else {
			entries = append(entries, jobEntry(name, now.Unix()))
		}
	}
	got := missedScanners(entries, now)
	if len(got) != 1 || got[0] != "popeye_scan" {
		t.Fatalf("expected only popeye_scan missed, got %v", got)
	}
}

func TestUpsertScheduleJobsArray_NewEntry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	out, prev := upsertScheduleJobsArray(nil, "popeye_scan", "0 12 * * 1", now)
	if prev != 0 {
		t.Fatalf("prevExec = %d; want 0 (no prior entry)", prev)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d; want 1", len(out))
	}
	entry := out[0].(map[string]any)
	if entry["runnable_name"] != scheduleJobRunnableName {
		t.Errorf("runnable_name = %q; want %q", entry["runnable_name"], scheduleJobRunnableName)
	}
	params := entry["runnable_params"].(map[string]any)
	if params["action_func_name"] != "popeye_scan" {
		t.Errorf("action_func_name = %q; want popeye_scan", params["action_func_name"])
	}
	sched := entry["scheduling_params"].(map[string]any)
	if sched["cron_expression"] != "0 12 * * 1" {
		t.Errorf("cron_expression = %q; want 0 12 * * 1", sched["cron_expression"])
	}
	state := entry["state"].(map[string]any)
	if state["exec_count"].(int) != 1 {
		t.Errorf("exec_count = %v; want 1 for new entry", state["exec_count"])
	}
	if state["last_exec_time_sec"].(int64) != now.Unix() {
		t.Errorf("last_exec_time_sec = %v; want %d", state["last_exec_time_sec"], now.Unix())
	}
	if state["job_status"].(int) != scheduleJobStatusDone {
		t.Errorf("job_status = %v; want %d", state["job_status"], scheduleJobStatusDone)
	}
}

func TestUpsertScheduleJobsArray_ReplaceIncrementsExecCount(t *testing.T) {
	existing := []any{
		map[string]any{
			"runnable_params": map[string]any{"action_func_name": "popeye_scan"},
			"state":           map[string]any{"exec_count": float64(5), "last_exec_time_sec": float64(100)},
		},
	}
	now := time.Unix(200, 0)
	out, prev := upsertScheduleJobsArray(existing, "popeye_scan", "0 12 * * 1", now)
	if prev != 5 {
		t.Errorf("prevExec = %d; want 5", prev)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d; want 1 (replace, not append)", len(out))
	}
	state := out[0].(map[string]any)["state"].(map[string]any)
	if state["exec_count"].(int) != 6 {
		t.Errorf("exec_count = %v; want 6 (5+1)", state["exec_count"])
	}
}

func TestUpsertScheduleJobsArray_PreservesOtherScanners(t *testing.T) {
	existing := []any{
		map[string]any{
			"runnable_params": map[string]any{"action_func_name": "trivy_cis_scan"},
			"state":           map[string]any{"exec_count": float64(3), "last_exec_time_sec": float64(50)},
		},
	}
	out, prev := upsertScheduleJobsArray(existing, "popeye_scan", "0 12 * * 1", time.Unix(200, 0))
	if prev != 0 {
		t.Errorf("prevExec = %d; want 0 (popeye is new)", prev)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d; want 2 (trivy preserved + popeye appended)", len(out))
	}
	// Find trivy and confirm untouched
	for _, e := range out {
		entry := e.(map[string]any)
		params := entry["runnable_params"].(map[string]any)
		if params["action_func_name"] == "trivy_cis_scan" {
			state := entry["state"].(map[string]any)
			if state["exec_count"].(float64) != 3 {
				t.Errorf("trivy exec_count = %v; want 3 (unchanged)", state["exec_count"])
			}
			if state["last_exec_time_sec"].(float64) != 50 {
				t.Errorf("trivy last_exec_time_sec = %v; want 50 (unchanged)", state["last_exec_time_sec"])
			}
		}
	}
}

func TestLastExecForScanner(t *testing.T) {
	jobs := []any{
		jobEntry("popeye_scan", 1700000000),
		jobEntry("trivy_cis_scan", 1700001000),
	}
	if got := lastExecForScanner(jobs, "popeye_scan"); got != 1700000000 {
		t.Errorf("popeye = %d; want 1700000000", got)
	}
	if got := lastExecForScanner(jobs, "trivy_cis_scan"); got != 1700001000 {
		t.Errorf("trivy = %d; want 1700001000", got)
	}
	if got := lastExecForScanner(jobs, "kube_bench_scan"); got != 0 {
		t.Errorf("absent scanner = %d; want 0", got)
	}
}

func TestScheduledScanners_AllHaveCron(t *testing.T) {
	for _, name := range scheduledScanners() {
		if cronExpressionForScanner(name) == "" {
			t.Errorf("scanner %q is in scheduledScanners() but has no cron expression", name)
		}
	}
}

func TestCronNextAfter_WeeklyCron(t *testing.T) {
	// popeye cron "0 12 * * 1" — Monday at 12:00.
	// Baseline: 2026-01-05 (Monday) 11:00 → next tick is same-day 12:00.
	baseline := time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC)
	got, err := cronNextAfter("0 12 * * 1", baseline)
	if err != nil {
		t.Fatalf("ParseStandard error: %v", err)
	}
	want := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("next = %v; want %v", got, want)
	}
}

func TestCronNextAfter_InvalidExpression(t *testing.T) {
	_, err := cronNextAfter("not a cron", time.Now())
	if err == nil {
		t.Error("expected error for invalid cron expression; got nil")
	}
}
