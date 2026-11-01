package api

import (
	"errors"
	"fmt"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// remediationArtifacts: code_fix only when the server-read evidence has one; the rest come from the caller.
func remediationArtifacts(claimed []string, hasCodeFix bool) []string {
	out := []string{}
	for _, a := range claimed {
		if a != artifactCodeFix {
			out = append(out, a)
		}
	}
	if hasCodeFix {
		out = append(out, artifactCodeFix)
	}
	return out
}

// artifactCodeFix names the code-fix surface (RemediationPanel.jsx pushes the same string).
const artifactCodeFix = "code_fix"

// evidenceFromStoredAnalysis is recorded on the generation trace: the evidence was read server-side.
const evidenceFromStoredAnalysis = "stored_analysis"

// clientOnlyHeadings: context blocks only the browser has (triage threshold, alert-rule controls).
var clientOnlyHeadings = []string{
	"## Threshold suggestion already computed by triage",
	"## Alert rule this event came from",
}

// maxCodeFixDiff matches the client's cap (RemediationPanel.jsx MAX_DIFF_CHARS).
const maxCodeFixDiff = 4000

// remediationEvidence builds a plan's evidence from this event's stored analysis (any age) plus the
// caller's client-only blocks; caller free text once planned a cart patch on a benchmark-server alert.
func remediationEvidence(ctx *security.RequestContext, accountId, eventId, clientContext string) (evidence string, hasCodeFix bool, err error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", false, fmt.Errorf("database unavailable: %w", err)
	}
	repo := events.NewEventAnalysisRepositoryForAccount(ctx, dbManager, accountId)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil {
		return "", false, fmt.Errorf("event lookup failed: %w", err)
	}
	if info == nil {
		return "", false, errRemediationInput{"event not found"}
	}
	fp := firstNonEmpty(info.Fingerprint, eventId)
	var readErr error // a failed read is an outage, not a missing stage
	completed := func(t events.EventAnalysisType) *events.EventAnalysis {
		row, err := repo.GetEventAnalysis(ctx, eventId, fp, info.AggregationKey, accountId, t)
		if err != nil && readErr == nil {
			readErr = fmt.Errorf("reading %s: %w", t, err)
		}
		if err != nil || row == nil || row.Status != string(events.AnalysisStatusCompleted) {
			return nil
		}
		return row
	}
	rec, loadErr := loadRemediationAnalysis(completed)
	if readErr != nil {
		return "", false, readErr
	}
	if loadErr != nil {
		// No completed investigation: nothing to plan from. A threshold or alert-rule block alone isn't one.
		return "", false, nil
	}
	stored := storedAnalysisEvidence(rec)
	return strings.Join(nonEmpty([]string{stored, clientOnlySections(clientContext)}), "\n\n"), codeFixEvidence(rec) != "", nil
}

// loadRemediationAnalysis needs only a completed investigation; other stages are used when complete
// (requiring all four left 44/2,110 dev events planless). completed -> row if COMPLETED, else nil.
func loadRemediationAnalysis(completed func(events.EventAnalysisType) *events.EventAnalysis) (EventAnalysisResponse, error) {
	var rec EventAnalysisResponse
	inv := completed(events.AnalysisTypeInvestigation)
	if inv == nil || strings.TrimSpace(inv.Summary) == "" {
		return rec, errors.New("investigation is not complete")
	}
	rec.Investigation = inv.Summary
	if logRow := completed(events.AnalysisTypeLog); logRow != nil {
		var logResp EventAnalysisResponse
		if err := common.UnmarshalJson([]byte(logRow.Analysis), &logResp); err == nil {
			copyLogAnalysisFields(&rec, logResp)
		}
		rec.Summary = logRow.Summary
	}
	if summary := completed(events.AnalysisTypeSummary); summary != nil && summary.Summary != "" {
		rec.Summary = summary.Summary
	}
	if report := completed(events.AnalysisTypeDetailedResponse); report != nil {
		rec.DetailedResponse = report.Summary
	}
	return rec, nil
}

// storedAnalysisEvidence assembles the analysis the way RemediationPanel.jsx generate() does, so a
// plan reads the same evidence whichever side builds it.
func storedAnalysisEvidence(rec EventAnalysisResponse) string {
	investigation := rec.Investigation
	// #38925: the report opens with the investigation verbatim; sending both repeats it.
	if inv := strings.TrimSpace(investigation); inv != "" && strings.HasPrefix(strings.TrimSpace(rec.DetailedResponse), inv) {
		investigation = ""
	}
	return strings.Join(nonEmpty([]string{rec.Summary, investigation, rec.DetailedResponse, rec.RootCauseAnalysis, codeFixEvidence(rec)}), "\n\n")
}

// codeFixEvidence mirrors formatCodeFixContext in RemediationPanel.jsx.
func codeFixEvidence(rec EventAnalysisResponse) string {
	files := stringList(rec.FilesModified)
	diff := strings.TrimSpace(rec.GitDiff)
	original := strings.TrimSpace(rec.OriginalCode)
	prURL := strings.TrimSpace(rec.AutomatedFixPR.URL)
	if len(files) == 0 && diff == "" && prURL == "" {
		return ""
	}
	lines := []string{"## Code fix already identified by code analysis",
		"This fix was derived from the source and is the durable fix for the root cause. Do not re-derive it."}
	if len(files) > 0 {
		lines = append(lines, "Files: "+strings.Join(files, ", "))
	}
	if prURL != "" {
		lines = append(lines, "An automated fix PR already exists: "+prURL)
	}
	if original != "" {
		lines = append(lines, "Current code:\n"+original)
	}
	if diff != "" {
		if len(diff) > maxCodeFixDiff {
			diff = diff[:maxCodeFixDiff] + "\n[diff truncated]"
		}
		lines = append(lines, "Proposed diff:\n"+diff)
	}
	return strings.Join(lines, "\n")
}

// stringList reads files_modified, which is stored as a JSON array of any shape.
func stringList(v any) []string {
	out := []string{}
	switch list := v.(type) {
	case []string:
		for _, x := range list {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	case []any:
		for _, x := range list {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

// clientOnlySections keeps the blocks of a client context that start with a client-only heading, each
// up to the next top-level heading.
func clientOnlySections(context string) string {
	kept := []string{}
	for _, heading := range clientOnlyHeadings {
		start := strings.Index(context, heading)
		if start < 0 {
			continue
		}
		block := context[start:]
		if end := strings.Index(block[len(heading):], "\n\n## "); end >= 0 {
			block = block[:len(heading)+end]
		}
		kept = append(kept, strings.TrimSpace(block))
	}
	return strings.Join(kept, "\n\n")
}
