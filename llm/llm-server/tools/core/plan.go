package core

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Structured plan carried by the update_notebook_v2 control tool (#41443).
// It lives in tools/core because both the tool (package tools, which validates
// a call and reports errors back to the model) and the ReAct4 planner (package
// agents/core, which applies the patches and enforces the finish check) need it,
// and both import this package.

// Plan step kinds. A task step finishes as done/skipped/blocked; a hypothesis
// step is a claim under test and finishes as supported/refuted/inconclusive.
const (
	PlanStepKindTask       = "task"
	PlanStepKindHypothesis = "hypothesis"
)

// Plan step statuses. The task and hypothesis sets are disjoint, so a status
// alone identifies the kind it belongs to.
const (
	PlanStatusPending    = "pending"
	PlanStatusInProgress = "in_progress"
	PlanStatusDone       = "done"
	PlanStatusSkipped    = "skipped"
	PlanStatusBlocked    = "blocked"

	PlanStatusOpen         = "open"
	PlanStatusTesting      = "testing"
	PlanStatusSupported    = "supported"
	PlanStatusRefuted      = "refuted"
	PlanStatusInconclusive = "inconclusive"
)

// PlanTaskStatuses and PlanHypothesisStatuses list the valid statuses per kind,
// in the order they are advertised in the tool schema.
var (
	PlanTaskStatuses       = []string{PlanStatusPending, PlanStatusInProgress, PlanStatusDone, PlanStatusSkipped, PlanStatusBlocked}
	PlanHypothesisStatuses = []string{PlanStatusOpen, PlanStatusTesting, PlanStatusSupported, PlanStatusRefuted, PlanStatusInconclusive}
	PlanPriors             = []string{"high", "medium", "low"}
)

// PlanStep is one step of the plan. In a patch only ID is required; empty
// fields leave the stored value unchanged.
type PlanStep struct {
	ID      string `json:"id"`
	Phase   string `json:"phase,omitempty"`
	Title   string `json:"title,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status,omitempty"`
	Prior   string `json:"prior,omitempty"`
	Finding string `json:"finding,omitempty"`
	Parent  string `json:"parent,omitempty"`
}

// PlanStepsArgument is the update_notebook_v2 argument carrying the patch list.
const PlanStepsArgument = "steps"

// PlanStatusIsClosing reports whether status ends a step.
func PlanStatusIsClosing(status string) bool {
	switch status {
	case PlanStatusDone, PlanStatusSkipped, PlanStatusBlocked,
		PlanStatusSupported, PlanStatusRefuted, PlanStatusInconclusive:
		return true
	}
	return false
}

// PlanStatusIsActive reports whether status marks the step being worked now.
func PlanStatusIsActive(status string) bool {
	return status == PlanStatusInProgress || status == PlanStatusTesting
}

// PlanStepIsOpen reports whether a step still needs work before an answer.
func PlanStepIsOpen(step PlanStep) bool {
	return !PlanStatusIsClosing(step.Status)
}

func planKindForStatus(status string) string {
	switch {
	case slices.Contains(PlanHypothesisStatuses, status):
		return PlanStepKindHypothesis
	case slices.Contains(PlanTaskStatuses, status):
		return PlanStepKindTask
	}
	return ""
}

// ParsePlanPatches decodes and validates the arguments of one
// update_notebook_v2 call ({"steps":[...]}). Errors are written for the model:
// they say what to change.
func ParsePlanPatches(args string) ([]PlanStep, error) {
	var payload struct {
		Steps []PlanStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object with a 'steps' array: %w", err)
	}
	if len(payload.Steps) == 0 {
		return nil, fmt.Errorf("'steps' must contain at least one step to add or update")
	}
	for i := range payload.Steps {
		s := &payload.Steps[i]
		// Ids are case-insensitive: the plan shows hypotheses as H1 (the format
		// the critiquer reads) while the model may patch them as h1.
		s.ID = strings.ToLower(strings.TrimSpace(s.ID))
		s.Parent = strings.ToLower(strings.TrimSpace(s.Parent))
		s.Finding = strings.TrimSpace(s.Finding)
		s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
		s.Status = strings.ToLower(strings.TrimSpace(s.Status))
		s.Prior = strings.ToLower(strings.TrimSpace(s.Prior))
		if s.ID == "" {
			return nil, fmt.Errorf("step %d: 'id' is required", i+1)
		}
		if s.Kind != "" && s.Kind != PlanStepKindTask && s.Kind != PlanStepKindHypothesis {
			return nil, fmt.Errorf("step %q: kind must be %q or %q", s.ID, PlanStepKindTask, PlanStepKindHypothesis)
		}
		if s.Status != "" {
			kind := planKindForStatus(s.Status)
			if kind == "" {
				return nil, fmt.Errorf("step %q: unknown status %q; tasks use %s, hypotheses use %s",
					s.ID, s.Status, strings.Join(PlanTaskStatuses, "/"), strings.Join(PlanHypothesisStatuses, "/"))
			}
			if s.Kind != "" && s.Kind != kind {
				return nil, fmt.Errorf("step %q: status %q belongs to a %s, not a %s", s.ID, s.Status, kind, s.Kind)
			}
			if PlanStatusIsClosing(s.Status) && s.Finding == "" {
				return nil, fmt.Errorf("step %q: closing it as %q needs a one-line 'finding' saying what was found or why", s.ID, s.Status)
			}
		}
		if s.Prior != "" && !slices.Contains(PlanPriors, s.Prior) {
			return nil, fmt.Errorf("step %q: prior must be high, medium or low", s.ID)
		}
	}
	return payload.Steps, nil
}

// ApplyPlanPatches returns plan with patches applied in order. A patch for a
// known id overwrites the fields it sets; a patch for a new id adds a step and
// needs a title (otherwise it is ignored). Kind defaults from the status, then
// to task; status defaults to pending (task) or open (hypothesis). Nesting is one
// level: a parent that is unknown or is itself a child is dropped.
func ApplyPlanPatches(plan []PlanStep, patches []PlanStep) []PlanStep {
	out, _ := ApplyPlanPatchesReport(plan, patches)
	return out
}

// PlanClosingStatuses names the statuses that close a step of kind.
func PlanClosingStatuses(kind string) string {
	if kind == PlanStepKindHypothesis {
		return "supported, refuted or inconclusive"
	}
	return "done, skipped or blocked"
}

// ApplyPlanPatchesReport is ApplyPlanPatches that also returns one line per
// patch status it ignored, so the model can be told its update did not land.
func ApplyPlanPatchesReport(plan []PlanStep, patches []PlanStep) ([]PlanStep, []string) {
	var ignored []string
	out := make([]PlanStep, len(plan))
	copy(out, plan)
	index := make(map[string]int, len(out))
	for i, s := range out {
		index[s.ID] = i
	}
	for _, p := range patches {
		i, ok := index[p.ID]
		if !ok {
			// A new step without a title keeps its id as the title rather than
			// being dropped: the tool reported success, so the step must show up
			// in the plan the model sees next.
			title := strings.TrimSpace(p.Title)
			if title == "" {
				title = p.ID
			}
			out = append(out, PlanStep{ID: p.ID, Title: title})
			i = len(out) - 1
			index[p.ID] = i
		}
		s := &out[i]
		if p.Phase != "" {
			s.Phase = p.Phase
		}
		if p.Title != "" {
			s.Title = p.Title
		}
		if p.Kind != "" {
			s.Kind = p.Kind
		}
		// A status from the other kind does not change a known step's kind: a
		// status-only "done" on hypothesis h1 would otherwise turn it into a task
		// and hide it from the critiquer's hypothesis check. The status is
		// ignored, so the step stays open and the finish check asks again.
		if p.Status != "" && (ok && p.Kind == "" && s.Kind != "" && planKindForStatus(p.Status) != s.Kind) {
			ignored = append(ignored, fmt.Sprintf("%s is a %s, so status %q was not applied; close it as %s",
				p.ID, s.Kind, p.Status, PlanClosingStatuses(s.Kind)))
			p.Status = ""
		}
		if p.Status != "" {
			s.Status = p.Status
			if s.Kind == "" || s.Kind != planKindForStatus(p.Status) {
				s.Kind = planKindForStatus(p.Status)
			}
		}
		if p.Prior != "" {
			s.Prior = p.Prior
		}
		if p.Finding != "" {
			s.Finding = p.Finding
		}
		if p.Parent != "" {
			s.Parent = p.Parent
		}
		if s.Kind == "" {
			s.Kind = PlanStepKindTask
		}
		if s.Status == "" || planKindForStatus(s.Status) != s.Kind {
			if s.Kind == PlanStepKindHypothesis {
				s.Status = PlanStatusOpen
			} else {
				s.Status = PlanStatusPending
			}
		}
	}
	// Nesting is decided in two order-independent passes: first drop parents
	// that do not exist or are the step itself, then, against that cleaned
	// snapshot, drop any parent that is itself nested (one level only, so
	// cycles never survive).
	parents := make([]string, len(out))
	for i := range out {
		if _, ok := index[out[i].Parent]; ok && out[i].Parent != out[i].ID {
			parents[i] = out[i].Parent
		}
	}
	for i := range out {
		if parents[i] == "" || parents[index[parents[i]]] != "" {
			out[i].Parent = ""
		}
	}
	return out, ignored
}
