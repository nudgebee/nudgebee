package core

import (
	"sync"
	"time"
)

// maxRecordedSteps caps how many sub-steps one tool call keeps. A resource
// lookup that misses the inventory can issue ~25 relay commands; beyond that the
// list stops being a debugging aid and starts being a storage problem. Steps
// past the cap are counted, never silently dropped — see ToolCallStats.Dropped.
const maxRecordedSteps = 40

// ToolCallStep is one downstream operation a tool performed: an inventory-DB
// query or a command executed in the cluster. Persisted as its own
// llm_conversation_tool_calls row (linked to the parent via
// metadata.parent_tool_call_id) so the conversation UI can show what actually
// ran, with its input and output, without anyone opening a SQL client.
type ToolCallStep struct {
	// Kind is "db" or "relay".
	Kind string
	// Command is the statement or shell command as executed — for DB steps a
	// readable rendering (the SQL text is a constant; the patterns are the
	// informative part).
	Command string
	// Output is what came back. Stored in full: the whole point is not having to
	// go to the logs for it.
	Output string
	// Err is the failure text when the step failed, empty otherwise.
	Err      string
	Duration time.Duration
}

// ToolCallStats accumulates where a single tool call spent its time and what it
// actually ran. A tool's total duration cannot distinguish an inventory-DB hit
// from a fallback to the live kubectl cascade — the two differ by an order of
// magnitude and by which fix applies.
//
// Every field is guarded by the one mutex because tools fan their downstream
// work out across goroutines (resource_search runs four search strategies
// concurrently), so writes race by construction. Counters and steps must move
// together: split across atomics and a lock, a reader can observe two queries
// counted but three steps recorded.
//
// A nil *ToolCallStats is a valid no-op receiver: tools that don't record, and
// call sites that build an NbToolContext without one, cost nothing.
type ToolCallStats struct {
	mu         sync.Mutex
	dbQueries  int
	dbNanos    int64
	relayCalls int
	relayNanos int64
	steps      []ToolCallStep
	dropped    int
	// Wall-clock span per kind, tracked as earliest-start / latest-end. Summed
	// durations and elapsed time are the same thing only while calls are
	// sequential; once a tool runs them concurrently the sum exceeds the wall
	// clock and reading it as latency inverts the answer — parallelising
	// enrichWithOwners cut the tool from 3.7s to 1.9s while the summed relay
	// figure *rose* from 3.1s to 5.7s. Both numbers are useful, but only one is
	// latency.
	dbSpan    span
	relaySpan span
}

// span accumulates the earliest start and latest end of a set of operations, so
// overlapping work is measured as elapsed time rather than summed work.
type span struct {
	first time.Time
	last  time.Time
}

func (sp *span) add(start, end time.Time) {
	if sp.first.IsZero() || start.Before(sp.first) {
		sp.first = start
	}
	if end.After(sp.last) {
		sp.last = end
	}
}

func (sp span) millis() int64 {
	if sp.first.IsZero() {
		return 0
	}
	return sp.last.Sub(sp.first).Milliseconds()
}

// RecordDB adds one database round-trip.
func (s *ToolCallStats) RecordDB(command, output string, err error, d time.Duration) {
	if s == nil {
		return
	}
	end := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dbQueries++
	s.dbNanos += int64(d)
	s.dbSpan.add(end.Add(-d), end) // span covers every call, including ones the cap drops
	if len(s.steps) >= maxRecordedSteps {
		s.dropped++
		return
	}
	s.steps = append(s.steps, ToolCallStep{Kind: "db", Command: command, Output: output, Err: errText(err), Duration: d})
}

// RecordRelay adds one relay round-trip (a command executed in the customer
// cluster).
func (s *ToolCallStats) RecordRelay(command, output string, err error, d time.Duration) {
	if s == nil {
		return
	}
	end := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.relayCalls++
	s.relayNanos += int64(d)
	s.relaySpan.add(end.Add(-d), end) // span covers every call, including ones the cap drops
	if len(s.steps) >= maxRecordedSteps {
		s.dropped++
		return
	}
	s.steps = append(s.steps, ToolCallStep{Kind: "relay", Command: command, Output: output, Err: errText(err), Duration: d})
}

// Steps returns a copy of the recorded steps plus how many were dropped by the
// cap. The count is returned rather than hidden so a caller can say "40 shown,
// 6 more not recorded" instead of presenting a truncated list as complete.
func (s *ToolCallStats) Steps() ([]ToolCallStep, int) {
	if s == nil {
		return nil, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolCallStep, len(s.steps))
	copy(out, s.steps)
	return out, s.dropped
}

// ApplyTo writes the accumulated counters onto a response's metadata, creating
// the metadata block if the tool didn't set one. No-ops when nothing was
// recorded, keeping the persisted JSONB free of zero-valued noise.
func (s *ToolCallStats) ApplyTo(meta *NBToolResponseMetadata) *NBToolResponseMetadata {
	if s == nil {
		return meta
	}
	// Everything is read under one lock so the snapshot describes a single
	// moment; calling s.Steps() here would take the lock a second time.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dbQueries == 0 && s.relayCalls == 0 {
		return meta
	}
	if meta == nil {
		meta = &NBToolResponseMetadata{}
	}

	meta.DBQueries = s.dbQueries
	meta.DBMs = s.dbSpan.millis()
	meta.DBBusyMs = s.dbNanos / int64(time.Millisecond)
	meta.RelayCalls = s.relayCalls
	meta.RelayMs = s.relaySpan.millis()
	meta.RelayBusyMs = s.relayNanos / int64(time.Millisecond)

	steps := make([]ToolCallStep, len(s.steps))
	copy(steps, s.steps)
	meta.Steps, meta.StepsDropped = steps, s.dropped
	return meta
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
