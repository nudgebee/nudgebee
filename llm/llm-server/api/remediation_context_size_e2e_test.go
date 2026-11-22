//go:build e2e

package api

import (
	"sort"
	"strings"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Measures the facts block against real events, so the budget is set from evidence rather than
// guessed. Run with: go test ./api -tags e2e -run TestFactsBlockSizes -v -timeout 1800s
func TestFactsBlockSizes(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT e.id, e.cloud_account_id
		  FROM events e
		 WHERE COALESCE(e.subject_name,'') <> ''
		 ORDER BY e.starts_at DESC NULLS LAST
		 LIMIT 35`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	sizes := []int{}
	capped, empty := 0, 0
	sectionTotal := map[string]int{}
	sectionMax := map[string]int{}
	for rows.Next() {
		var eventId, accountId string
		if err := rows.Scan(&eventId, &accountId); err != nil {
			continue
		}
		facts, _ := buildRemediationFacts(ctx, accountId, eventId)
		if facts == "" {
			empty++
			continue
		}
		sizes = append(sizes, len(facts))
		if strings.Contains(facts, "[facts truncated]") {
			capped++
		}
		for _, section := range strings.Split(facts, "\n\n") {
			name := strings.SplitN(section, "\n", 2)[0]
			if len(name) > 38 {
				name = name[:38]
			}
			sectionTotal[name] += len(section)
			if len(section) > sectionMax[name] {
				sectionMax[name] = len(section)
			}
		}
	}
	sort.Ints(sizes)
	if len(sizes) == 0 {
		t.Fatal("no facts built")
	}
	at := func(q float64) int { return sizes[int(float64(len(sizes)-1)*q)] }
	t.Logf("SIZES n=%d empty=%d capped=%d min=%d p50=%d p90=%d p99=%d max=%d budget=%d",
		len(sizes), empty, capped, sizes[0], at(0.5), at(0.9), at(0.99), sizes[len(sizes)-1], factsBudget)
	for name, total := range sectionTotal {
		t.Logf("SECTION %-40s avg=%d max=%d", name, total/len(sizes), sectionMax[name])
	}
}
