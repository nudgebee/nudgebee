package flow_sources

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// TestTranslateDuplicateError verifies the unique-violation translation:
// only SQLSTATE 23505 + matching constraint name maps to
// ErrManualDependencyDuplicate; everything else passes through wrapped with
// the supplied prefix.
func TestTranslateDuplicateError(t *testing.T) {
	dep := ManualDependency{
		SourceNodeType:   "K8sService",
		SourceName:       "app-prod",
		SourceNamespace:  "nudgebee",
		SourceCluster:    "k8s-prod",
		DestNodeType:     "K8sService",
		DestName:         "auto-pilot-server",
		DestNamespace:    "nudgebee",
		DestCluster:      "k8s-prod",
		RelationshipType: "CALLS",
	}

	t.Run("nil error returns nil", func(t *testing.T) {
		if got := translateDuplicateError(nil, dep, "insert manual dependency"); got != nil {
			t.Errorf("translateDuplicateError(nil) = %v, want nil", got)
		}
	})

	t.Run("duplicate on dedupe index → ErrManualDependencyDuplicate with endpoint tuple", func(t *testing.T) {
		pqErr := &pq.Error{
			Code:       pgUniqueViolationCode,
			Constraint: ManualDependencyDedupeIndex,
			Message:    `duplicate key value violates unique constraint "idx_kg_manual_deps_dedupe"`,
		}
		got := translateDuplicateError(pqErr, dep, "insert manual dependency")
		if !errors.Is(got, ErrManualDependencyDuplicate) {
			t.Errorf("expected ErrManualDependencyDuplicate wrap, got %v", got)
		}
		// Endpoint tuple should appear so the operator can recognize the row.
		msg := got.Error()
		for _, want := range []string{"K8sService", "nudgebee/app-prod@k8s-prod", "nudgebee/auto-pilot-server@k8s-prod", "CALLS"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error message missing %q; got %q", want, msg)
			}
		}
	})

	t.Run("other 23505 (different constraint) passes through wrapped", func(t *testing.T) {
		pqErr := &pq.Error{
			Code:       pgUniqueViolationCode,
			Constraint: "some_other_unique_constraint",
			Message:    `duplicate key value violates unique constraint "some_other_unique_constraint"`,
		}
		got := translateDuplicateError(pqErr, dep, "insert manual dependency")
		if errors.Is(got, ErrManualDependencyDuplicate) {
			t.Error("non-dedupe-constraint 23505 should NOT be classified as ErrManualDependencyDuplicate")
		}
		if !strings.HasPrefix(got.Error(), "insert manual dependency: ") {
			t.Errorf("expected 'insert manual dependency' prefix, got %q", got.Error())
		}
	})

	t.Run("non-pq error passes through wrapped", func(t *testing.T) {
		raw := errors.New("connection refused")
		got := translateDuplicateError(raw, dep, "insert manual dependency")
		if errors.Is(got, ErrManualDependencyDuplicate) {
			t.Error("non-pq error should not classify as duplicate")
		}
		if !strings.Contains(got.Error(), "connection refused") {
			t.Errorf("expected original error preserved, got %q", got.Error())
		}
	})

	t.Run("wrapped duplicate (errors.As) still detected", func(t *testing.T) {
		// Real callers receive the pq.Error wrapped by sqlx / fmt.Errorf —
		// translateDuplicateError uses errors.As so it must unwrap.
		pqErr := &pq.Error{Code: pgUniqueViolationCode, Constraint: ManualDependencyDedupeIndex}
		wrapped := fmt.Errorf("sqlx scan failed: %w", pqErr)
		got := translateDuplicateError(wrapped, dep, "insert manual dependency returning id")
		if !errors.Is(got, ErrManualDependencyDuplicate) {
			t.Errorf("wrapped pq.Error should still be classified as duplicate; got %v", got)
		}
	})
}

// TestBuildUpdateFromResultsPartialPick pins the partial-pick contract used by
// SetResolvedNodes/resolveAndPinPartial: when one side is operator-pinned
// (status=Resolved, candidates nil), the row-level status folds correctly
// against the resolver's outcome on the OTHER side without re-resolving the
// pinned side. This guards against a future refactor that accidentally puts
// the pinned NodeID through the resolver again.
func TestBuildUpdateFromResultsPartialPick(t *testing.T) {
	pinSrc := &EndpointResolveResult{Status: EndpointStatusResolved, NodeID: "src-uuid", MatchCount: 1}
	pinDst := &EndpointResolveResult{Status: EndpointStatusResolved, NodeID: "dst-uuid", MatchCount: 1}

	cases := []struct {
		name           string
		src, dst       *EndpointResolveResult
		wantStatus     string
		wantSrcNode    string
		wantDstNode    string
		wantSrcMatches int
		wantDstMatches int
	}{
		{
			name:           "both pinned -> fully resolved",
			src:            pinSrc,
			dst:            pinDst,
			wantStatus:     ManualResolutionResolved,
			wantSrcNode:    "src-uuid",
			wantDstNode:    "dst-uuid",
			wantSrcMatches: 1,
			wantDstMatches: 1,
		},
		{
			name: "source pinned, dest resolves cleanly",
			src:  pinSrc,
			dst: &EndpointResolveResult{
				Status: EndpointStatusResolved, NodeID: "dst-resolved-uuid", MatchCount: 1,
			},
			wantStatus:     ManualResolutionResolved,
			wantSrcNode:    "src-uuid",
			wantDstNode:    "dst-resolved-uuid",
			wantSrcMatches: 1,
			wantDstMatches: 1,
		},
		{
			name: "source pinned, dest still ambiguous -> dest_ambiguous",
			src:  pinSrc,
			dst: &EndpointResolveResult{
				Status: EndpointStatusAmbiguous,
				Candidates: []ManualMatchCandidate{
					{NodeID: "d1", DisplayName: "d1"},
					{NodeID: "d2", DisplayName: "d2"},
				},
				MatchCount: 2,
			},
			wantStatus:     ManualResolutionDestAmbiguous,
			wantSrcNode:    "src-uuid",
			wantDstNode:    "",
			wantSrcMatches: 1,
			wantDstMatches: 2,
		},
		{
			name: "dest pinned, source still unmatched -> source_unmatched",
			src: &EndpointResolveResult{
				Status: EndpointStatusUnmatched, MatchCount: 0,
			},
			dst:            pinDst,
			wantStatus:     ManualResolutionSourceUnmatched,
			wantSrcNode:    "",
			wantDstNode:    "dst-uuid",
			wantSrcMatches: 0,
			wantDstMatches: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildUpdateFromResults(42, tc.src, tc.dst)
			if got.ID != 42 {
				t.Errorf("ID = %d, want 42", got.ID)
			}
			if got.ResolutionStatus != tc.wantStatus {
				t.Errorf("ResolutionStatus = %q, want %q", got.ResolutionStatus, tc.wantStatus)
			}
			if got.ResolvedSourceNodeID != tc.wantSrcNode {
				t.Errorf("ResolvedSourceNodeID = %q, want %q", got.ResolvedSourceNodeID, tc.wantSrcNode)
			}
			if got.ResolvedDestNodeID != tc.wantDstNode {
				t.Errorf("ResolvedDestNodeID = %q, want %q", got.ResolvedDestNodeID, tc.wantDstNode)
			}
			if got.SourceMatchCount != tc.wantSrcMatches {
				t.Errorf("SourceMatchCount = %d, want %d", got.SourceMatchCount, tc.wantSrcMatches)
			}
			if got.DestMatchCount != tc.wantDstMatches {
				t.Errorf("DestMatchCount = %d, want %d", got.DestMatchCount, tc.wantDstMatches)
			}
		})
	}
}

// TestFormatEndpointKeyForError verifies the operator-facing endpoint
// rendering used in duplicate-error messages.
func TestFormatEndpointKeyForError(t *testing.T) {
	tests := []struct {
		name     string
		ns, cl   string
		arn      string
		nodeName string
		want     string
	}{
		{"arn always wins", "prod", "us-east-1", "arn:aws:rds:us-east-1:123:db:orders", "ignored", "arn:aws:rds:us-east-1:123:db:orders"},
		{"namespace + cluster", "prod", "k8s-prod", "", "svc", "prod/svc@k8s-prod"},
		{"namespace only", "prod", "", "", "svc", "prod/svc"},
		{"bare name fallback", "", "", "", "svc", "svc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatEndpointKeyForError(tc.nodeName, tc.ns, tc.cl, tc.arn); got != tc.want {
				t.Errorf("formatEndpointKeyForError = %q, want %q", got, tc.want)
			}
		})
	}
}
