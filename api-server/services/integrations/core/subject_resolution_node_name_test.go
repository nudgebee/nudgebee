package core

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestNodeNameCandidatesFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  []string
	}{
		{
			name:  "gke fqdn yields full host and short node name",
			title: "CPU usage is high for host gke-demo-pool-a1b2c3d4-x1y2.us-central1-a.c.demo-project.internal",
			want: []string{
				"gke-demo-pool-a1b2c3d4-x1y2.us-central1-a.c.demo-project.internal",
				"gke-demo-pool-a1b2c3d4-x1y2",
			},
		},
		{
			name:  "aws fqdn",
			title: "Disk pressure on ip-10-0-1-5.us-west-2.compute.internal",
			want:  []string{"ip-10-0-1-5.us-west-2.compute.internal", "ip-10-0-1-5"},
		},
		{
			name:  "plain words are not candidates",
			title: "CPU usage is high for host",
			want:  nil,
		},
		{
			name:  "trailing punctuation is trimmed",
			title: "Node worker-1. is down",
			want:  []string{"worker-1"},
		},
		{
			name:  "empty title",
			title: "",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nodeNameCandidatesFromTitle(tt.title))
		})
	}
}

func TestNodeNameCandidatesFromTitle_Capped(t *testing.T) {
	title := ""
	for i := 0; i < 50; i++ {
		title += "node-" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + " "
	}
	assert.Len(t, nodeNameCandidatesFromTitle(title), maxTitleNodeCandidates)
}

func TestTitleHasCompetingPodShapedToken(t *testing.T) {
	// A distinct pod-shaped token alongside the matched node is a real competitor.
	assert.True(t, titleHasCompetingPodShapedToken("Pod payments-api-7d9f8c-abcde evicted from node worker-1", "worker-1"))
	// worker-1 / master-0 are common ordinal node names and happen to satisfy the
	// same StatefulSet-pod shape as a real pod — excluded because they ARE the
	// matched node, not a second, competing identity.
	assert.False(t, titleHasCompetingPodShapedToken("CPU usage is high for host worker-1", "worker-1"))
	assert.False(t, titleHasCompetingPodShapedToken("Node worker-1 NotReady", "worker-1"))
	// The matched node's own first DNS label is excluded too, not just the exact form:
	// the title's "worker-1" token is the FQDN's label, not a second identity.
	assert.False(t, titleHasCompetingPodShapedToken("CPU high on worker-1.example.internal", "worker-1.example.internal"))
}

const titleNodeQuery = `FROM k8s_nodes`

func expectTitleNodeRows(rows *sqlmock.Rows) {
	pkgMock.ExpectQuery(titleNodeQuery).
		WithArgs(testTenant, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)
}

func nodeRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "cloud_account_id", "exact_match"})
}

func TestResolveNodeNameFromTitle(t *testing.T) {
	title := "CPU usage is high for host node-a.example.internal"
	accounts := []string{"acct-1", "acct-2"}

	t.Run("single node matched", func(t *testing.T) {
		expectTitleNodeRows(nodeRows().AddRow("node-a", "acct-1", true))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Equal(t, "node-a", name)
		assert.Equal(t, "acct-1", acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("same node name active in two accounts is ambiguous", func(t *testing.T) {
		// A node name active under more than one linked cloud account (e.g. two AWS
		// accounts sharing the default VPC's private IP range) must not resolve to
		// whichever account's row happens to be most recently updated.
		expectTitleNodeRows(nodeRows().AddRow("node-a", "acct-2", true).AddRow("node-a", "acct-1", true))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Empty(t, name)
		assert.Empty(t, acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("two different nodes is ambiguous", func(t *testing.T) {
		expectTitleNodeRows(nodeRows().AddRow("node-a", "acct-1", true).AddRow("node-b", "acct-1", true))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Empty(t, name)
		assert.Empty(t, acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("exact match wins over a colliding label-only match", func(t *testing.T) {
		// "node-a.example.internal" is an exact hit; a different node in another
		// region/account that merely shares the derived short label "node-a" must
		// not turn this into a false ambiguity.
		expectTitleNodeRows(nodeRows().
			AddRow("node-a.example.internal", "acct-1", true).
			AddRow("node-a.other-region.internal", "acct-2", false))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Equal(t, "node-a.example.internal", name)
		assert.Equal(t, "acct-1", acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("label-only matches stay ambiguous when nothing is exact", func(t *testing.T) {
		expectTitleNodeRows(nodeRows().
			AddRow("node-a.example.internal", "acct-1", false).
			AddRow("node-a.other-region.internal", "acct-2", false))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Empty(t, name)
		assert.Empty(t, acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("no rows", func(t *testing.T) {
		expectTitleNodeRows(nodeRows())
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Empty(t, name)
		assert.Empty(t, acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		pkgMock.ExpectQuery(titleNodeQuery).
			WithArgs(testTenant, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnError(errors.New("boom"))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), title, accounts)
		assert.Empty(t, name)
		assert.Empty(t, acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("no candidates or accounts skips the query", func(t *testing.T) {
		sc := ctxForTenant(t, testTenant)
		name, _ := ResolveNodeNameFromTitle(sc, "CPU usage is high for host", accounts)
		assert.Empty(t, name)
		name, _ = ResolveNodeNameFromTitle(sc, title, nil)
		assert.Empty(t, name)
	})

	t.Run("a distinct pod-shaped token alongside a resolved node defers to the LLM", func(t *testing.T) {
		// The node itself still resolves via the query, but the title's separate
		// "payments-api-7d9f8c-abcde" pod-shaped token means the real subject is
		// ambiguous, so the match is discarded.
		podTitle := "Pod payments-api-7d9f8c-abcde evicted from node node-a"
		expectTitleNodeRows(nodeRows().AddRow("node-a", "acct-1", true))
		name, acct := ResolveNodeNameFromTitle(ctxForTenant(t, testTenant), podTitle, accounts)
		assert.Empty(t, name)
		assert.Empty(t, acct)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}

func TestResolveSubjectFromTitleNodeName(t *testing.T) {
	t.Run("match sets node subject, kind and label", func(t *testing.T) {
		expectTitleNodeRows(nodeRows().AddRow("node-a", "acct-1", true))
		e := &EventIncomingWebhook{EventTitle: "Node node-a NotReady"}
		assert.True(t, ResolveSubjectFromTitleNodeName(ctxForTenant(t, testTenant), e, "acct-1"))
		assert.Equal(t, "node-a", e.EventSubjectName)
		assert.Equal(t, "node", e.EventSubjectKind)
		assert.Equal(t, "title_node_name", e.Investigation.Labels["nb_subject_resolution"])
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("miss leaves the event untouched", func(t *testing.T) {
		expectTitleNodeRows(nodeRows())
		e := &EventIncomingWebhook{EventTitle: "Node node-a NotReady"}
		assert.False(t, ResolveSubjectFromTitleNodeName(ctxForTenant(t, testTenant), e, "acct-1"))
		assert.Empty(t, e.EventSubjectName)
		assert.Empty(t, e.EventSubjectKind)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("a pod named alongside the node defers to the LLM", func(t *testing.T) {
		expectTitleNodeRows(nodeRows().AddRow("node-a", "acct-1", true))
		e := &EventIncomingWebhook{EventTitle: "Pod payments-api-7d9f8c-abcde evicted from node node-a"}
		assert.False(t, ResolveSubjectFromTitleNodeName(ctxForTenant(t, testTenant), e, "acct-1"))
		assert.Empty(t, e.EventSubjectName)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("empty title or account skips the query", func(t *testing.T) {
		sc := ctxForTenant(t, testTenant)
		assert.False(t, ResolveSubjectFromTitleNodeName(sc, nil, "acct-1"))
		assert.False(t, ResolveSubjectFromTitleNodeName(sc, &EventIncomingWebhook{}, "acct-1"))
		assert.False(t, ResolveSubjectFromTitleNodeName(sc, &EventIncomingWebhook{EventTitle: "Node node-a down"}, ""))
	})
}
