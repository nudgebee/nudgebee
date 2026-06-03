package flow_sources

import (
	"strings"
	"testing"
)

func TestClassifyCandidates(t *testing.T) {
	tests := []struct {
		name           string
		input          []ManualMatchCandidate
		wantStatus     EndpointResolveStatus
		wantNodeID     string
		wantCount      int
		wantCandidates int
	}{
		{
			name:       "zero matches → unmatched",
			input:      []ManualMatchCandidate{},
			wantStatus: EndpointStatusUnmatched,
			wantCount:  0,
		},
		{
			name:       "exactly one match → resolved, NodeID populated, no candidates list",
			input:      []ManualMatchCandidate{{NodeID: "node-1", DisplayName: "svc-a"}},
			wantStatus: EndpointStatusResolved,
			wantNodeID: "node-1",
			wantCount:  1,
		},
		{
			name: "five matches → ambiguous, candidates carried, count matches",
			input: []ManualMatchCandidate{
				{NodeID: "n1"}, {NodeID: "n2"}, {NodeID: "n3"}, {NodeID: "n4"}, {NodeID: "n5"},
			},
			wantStatus:     EndpointStatusAmbiguous,
			wantCount:      5,
			wantCandidates: 5,
		},
		{
			name:           "exactly MaxManualMatchCandidates → still ambiguous (boundary)",
			input:          generateCandidates(MaxManualMatchCandidates),
			wantStatus:     EndpointStatusAmbiguous,
			wantCount:      MaxManualMatchCandidates,
			wantCandidates: MaxManualMatchCandidates,
		},
		{
			name:       "MaxManualMatchCandidates+1 → too_many_matches, no candidates stored",
			input:      generateCandidates(MaxManualMatchCandidates + 1),
			wantStatus: EndpointStatusTooManyMatches,
			wantCount:  MaxManualMatchCandidates + 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCandidates(tc.input)
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.NodeID != tc.wantNodeID {
				t.Errorf("NodeID = %q, want %q", got.NodeID, tc.wantNodeID)
			}
			if got.MatchCount != tc.wantCount {
				t.Errorf("MatchCount = %d, want %d", got.MatchCount, tc.wantCount)
			}
			if len(got.Candidates) != tc.wantCandidates {
				t.Errorf("len(Candidates) = %d, want %d", len(got.Candidates), tc.wantCandidates)
			}
			if tc.wantStatus == EndpointStatusTooManyMatches && got.ErrorMessage == "" {
				t.Error("expected non-empty ErrorMessage for too_many_matches")
			}
		})
	}
}

func TestRowResolutionFromEndpoints(t *testing.T) {
	res := func(s EndpointResolveStatus) *EndpointResolveResult {
		return &EndpointResolveResult{Status: s}
	}
	tests := []struct {
		name string
		src  *EndpointResolveResult
		dst  *EndpointResolveResult
		want string
	}{
		{"both resolved → resolved", res(EndpointStatusResolved), res(EndpointStatusResolved), ManualResolutionResolved},
		{"invalid payload wins over everything", res(EndpointStatusInvalidPayload), res(EndpointStatusResolved), ManualResolutionInvalidPayload},
		{"dest invalid also wins", res(EndpointStatusResolved), res(EndpointStatusInvalidPayload), ManualResolutionInvalidPayload},
		{"source unmatched beats dest ambiguous", res(EndpointStatusUnmatched), res(EndpointStatusAmbiguous), ManualResolutionSourceUnmatched},
		{"dest unmatched when source resolved", res(EndpointStatusResolved), res(EndpointStatusUnmatched), ManualResolutionDestUnmatched},
		{"source too_many beats dest ambiguous", res(EndpointStatusTooManyMatches), res(EndpointStatusAmbiguous), ManualResolutionSourceTooManyMatches},
		{"dest too_many when source resolved", res(EndpointStatusResolved), res(EndpointStatusTooManyMatches), ManualResolutionDestTooManyMatches},
		{"source ambiguous", res(EndpointStatusAmbiguous), res(EndpointStatusResolved), ManualResolutionSourceAmbiguous},
		{"dest ambiguous when source resolved", res(EndpointStatusResolved), res(EndpointStatusAmbiguous), ManualResolutionDestAmbiguous},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RowResolutionFromEndpoints(tc.src, tc.dst); got != tc.want {
				t.Errorf("RowResolutionFromEndpoints = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildUpdateFromResults(t *testing.T) {
	src := &EndpointResolveResult{
		Status:     EndpointStatusResolved,
		NodeID:     "node-src",
		MatchCount: 1,
	}
	dst := &EndpointResolveResult{
		Status:     EndpointStatusAmbiguous,
		MatchCount: 3,
		Candidates: []ManualMatchCandidate{{NodeID: "n1"}, {NodeID: "n2"}, {NodeID: "n3"}},
	}

	got := buildUpdateFromResults(42, src, dst)

	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if got.ResolutionStatus != ManualResolutionDestAmbiguous {
		t.Errorf("ResolutionStatus = %q, want %q", got.ResolutionStatus, ManualResolutionDestAmbiguous)
	}
	if got.ResolvedSourceNodeID != "node-src" {
		t.Errorf("ResolvedSourceNodeID = %q, want node-src", got.ResolvedSourceNodeID)
	}
	if got.ResolvedDestNodeID != "" {
		t.Errorf("ResolvedDestNodeID = %q, want empty (ambiguous dest must not pin a node id)", got.ResolvedDestNodeID)
	}
	if got.SourceMatchCount != 1 || got.DestMatchCount != 3 {
		t.Errorf("counts = (%d, %d), want (1, 3)", got.SourceMatchCount, got.DestMatchCount)
	}
	if len(got.DestMatchCandidates) != 3 {
		t.Errorf("DestMatchCandidates = %d entries, want 3", len(got.DestMatchCandidates))
	}
}

func TestBuildUpdateFromResults_ErrorMessageComposition(t *testing.T) {
	src := &EndpointResolveResult{
		Status:       EndpointStatusTooManyMatches,
		MatchCount:   25,
		ErrorMessage: "more than 10 nodes matched",
	}
	dst := &EndpointResolveResult{
		Status:       EndpointStatusUnmatched,
		ErrorMessage: "no node found for arn",
	}

	got := buildUpdateFromResults(7, src, dst)

	if !strings.Contains(got.ResolutionError, "source: more than 10") {
		t.Errorf("ResolutionError missing source prefix; got %q", got.ResolutionError)
	}
	if !strings.Contains(got.ResolutionError, "dest: no node found") {
		t.Errorf("ResolutionError missing dest prefix; got %q", got.ResolutionError)
	}
}

func TestValidateEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint ManualEndpoint
		wantErr  string
	}{
		{"missing node_type", ManualEndpoint{Name: "svc"}, "source_node_type is required"},
		{"missing name", ManualEndpoint{NodeType: "K8sService"}, "source_name is required"},
		{"both present passes", ManualEndpoint{NodeType: "K8sService", Name: "svc"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEndpoint(EndpointSideSource, tc.endpoint)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error %q, got nil", tc.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestValidateRelationshipType(t *testing.T) {
	for _, rt := range []string{"CALLS", "PUBLISHES_TO", "SUBSCRIBES_TO"} {
		if err := ValidateRelationshipType(rt); err != nil {
			t.Errorf("%s rejected unexpectedly: %v", rt, err)
		}
	}
	for _, rt := range []string{"", "INVALID", "calls", "Calls", "OWNS"} {
		if err := ValidateRelationshipType(rt); err == nil {
			t.Errorf("expected %q to be rejected", rt)
		}
	}
}

func TestBuildDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		args     [5]string // name, namespace, cluster, arn, nodeType
		contains []string
	}{
		{"arn always wins", [5]string{"orders", "prod", "us-east", "arn:aws:rds:...", "Database"}, []string{"orders", "arn:aws:rds"}},
		{"namespace + cluster used for k8s", [5]string{"svc", "prod", "us-east-1", "", "K8sService"}, []string{"svc", "prod", "us-east-1", "K8sService"}},
		{"namespace only when no cluster", [5]string{"svc", "prod", "", "", "K8sService"}, []string{"svc", "prod", "K8sService"}},
		{"bare name fallback", [5]string{"orders", "", "", "", "Database"}, []string{"orders", "Database"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDisplayName(tc.args[0], tc.args[1], tc.args[2], tc.args[3], tc.args[4])
			for _, substr := range tc.contains {
				if !strings.Contains(got, substr) {
					t.Errorf("display name %q missing %q", got, substr)
				}
			}
		})
	}
}

// generateCandidates produces n synthetic ManualMatchCandidate entries with
// monotonically increasing NodeIDs.
func generateCandidates(n int) []ManualMatchCandidate {
	out := make([]ManualMatchCandidate, n)
	for i := 0; i < n; i++ {
		out[i] = ManualMatchCandidate{NodeID: "n" + string(rune('0'+i%10))}
	}
	return out
}
