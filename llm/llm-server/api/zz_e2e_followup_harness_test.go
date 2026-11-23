package api

import (
	"os"
	"strconv"
	"testing"
	"time"

	"nudgebee/llm/common"
)

// Local e2e harness only (never committed): runs the follow-up and planning jobs the way its leader job does, every 2 s
// for E2E_FOLLOWUP_SECONDS, so the local build claims due follow-ups before the dev leader's 1-minute tick.
func TestE2EFollowupHarness(t *testing.T) {
	if os.Getenv("E2E_FOLLOWUP") != "1" {
		t.Skip("set E2E_FOLLOWUP=1")
	}
	secs, _ := strconv.Atoi(os.Getenv("E2E_FOLLOWUP_SECONDS"))
	if secs <= 0 {
		secs = 60
	}
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(deadline) {
		if err := runDueFollowups(); err != nil {
			t.Logf("%s runDueFollowups: %v", time.Now().Format(time.TimeOnly), err)
		}
		if err := runDuePlannings(); err != nil {
			t.Logf("%s runDuePlannings: %v", time.Now().Format(time.TimeOnly), err)
		}
		time.Sleep(2 * time.Second)
	}
	// A turn this process started dies with it and the stranded sweep then marks the conversation KILLED,
	// which locks it. Wait (up to 15 min) for this worker's turns to end before exiting.
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	worker := "local:" + os.Getenv("LLM_SERVER_NAME")
	for wait := time.Now().Add(15 * time.Minute); time.Now().Before(wait); time.Sleep(5 * time.Second) {
		var live int
		if err := dbManager.Db.Get(&live, `select count(*) from llm_conversation_messages where worker_name = $1 and status = 'IN_PROGRESS'`, worker); err != nil {
			t.Logf("live turns: %v", err)
			continue
		}
		if live == 0 {
			return
		}
		t.Logf("%s waiting on %d running turn(s)", time.Now().Format(time.TimeOnly), live)
	}
	t.Errorf("turns still running after 15 min; the sweep will kill them")
}
