package flow_sources

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"nudgebee/services/config"

	_ "github.com/lib/pq"
)

// TestVerifyLBTargetMembership_AgainstLiveDB is an offline verification harness,
// NOT a CI unit test. It reads the real load-balancer rows discovery wrote and
// runs TargetGroupMemberIDs over their stored target groups, answering the one
// question the fixtures cannot: how much of a real estate can actually be served
// from rows, and how much still falls back to describe-target-health.
//
// That ratio is what decides the saving, so it is worth measuring per environment
// before quoting a number. Read-only: it opens no write transaction and builds no
// graph, so it cannot tombstone anything on a shared environment.
//
// Skipped unless KG_VERIFY_DB_URL is set, so `make test` / CI never touch a DB:
//
//	KG_VERIFY_DB_URL='postgresql://postgres:...@localhost:5432/nudgebee?sslmode=disable' \
//	go test ./knowledge_graph/flow_sources/ -run TestVerifyLBTargetMembership_AgainstLiveDB -v
func TestVerifyLBTargetMembership_AgainstLiveDB(t *testing.T) {
	dbURL := os.Getenv("KG_VERIFY_DB_URL")
	if dbURL == "" {
		t.Skip("KG_VERIFY_DB_URL not set — skipping live-DB verification harness")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	const q = `
		SELECT name, COALESCE(meta::text, '{}')
		FROM public.cloud_resourses
		WHERE is_active AND status = 'Active'
		  AND type IN ('application_loadbalancer', 'network_loadbalancer')`
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("query load balancers: %v", err)
	}
	defer func() { _ = rows.Close() }()

	prev := config.Config.FeatureKGLBTargetsFromRows
	config.Config.FeatureKGLBTargetsFromRows = true
	defer func() { config.Config.FeatureKGLBTargetsFromRows = prev }()

	var (
		lbs, lbsFullyServed, lbsNoTargetGroups, lbsFallingBack int
		tgs, tgsServed, tgsMissing, members                    int
	)
	for rows.Next() {
		var name, metaJSON string
		if err := rows.Scan(&name, &metaJSON); err != nil {
			t.Fatalf("scan: %v", err)
		}
		lbs++

		var meta struct {
			TargetGroups []map[string]interface{} `json:"TargetGroups"`
		}
		if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil || meta.TargetGroups == nil {
			lbsNoTargetGroups++
			t.Logf("no stored target groups, would use the CLI: %s", name)
			continue
		}

		servedAll := true
		for _, tg := range meta.TargetGroups {
			tgs++
			ids, ok := TargetGroupMemberIDs(tg)
			if !ok {
				tgsMissing++
				servedAll = false
				t.Logf("target group has no captured membership: %s / %v", name, tg["TargetGroupName"])
				continue
			}
			tgsServed++
			members += len(ids)
			for _, id := range ids {
				if id == "" {
					t.Errorf("blank target id on %s / %v", name, tg["TargetGroupName"])
				}
			}
		}
		if servedAll {
			lbsFullyServed++
		} else {
			lbsFallingBack++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if lbs == 0 {
		t.Skip("no active load balancers in this environment — nothing to verify")
	}

	t.Logf("load balancers=%d fully_served=%d falling_back=%d no_target_groups=%d",
		lbs, lbsFullyServed, lbsFallingBack, lbsNoTargetGroups)
	t.Logf("target groups=%d served_from_row=%d missing_membership=%d members=%d",
		tgs, tgsServed, tgsMissing, members)

	if tgsServed == 0 {
		t.Fatalf("no target group was served from a row — either discovery has not captured membership here, or the reader is broken")
	}
}
