package egressfilter

import (
	"context"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

// representativeRunbook is a realistic mid-size LLM payload — system prompt +
// runbook + a few tool observations — used to measure the egressfilter on a
// payload shape similar to what the planner sends in production. ~3 KB.
var representativeRunbook = strings.Repeat(
	"Investigate pod restarts in namespace prod. The deployment foo-api has "+
		"3 replicas. Recent kubectl output shows pod foo-api-7b9c-xyz12 entered "+
		"CrashLoopBackOff at 2026-01-12T10:14:00Z. The previous container exited "+
		"with code 137 (OOMKilled). Memory limit is 512Mi; usage at termination "+
		"was 511Mi. Suggested action: increase memory limit and re-roll. ",
	6,
)

// representativePayloadWithSecret simulates a leaked credential mid-payload.
var representativePayloadWithSecret = representativeRunbook +
	"\nDEBUG: AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n" + representativeRunbook

// representativeLargePayload approximates a context-window-sized payload
// (~32 KB) — closer to the upper bound of what we send.
var representativeLargePayload = strings.Repeat(representativeRunbook, 11)

func BenchmarkScan_Clean_Small(b *testing.B) {
	payload := "What is the status of pod foo in namespace prod?"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Scan(payload)
	}
}

func BenchmarkScan_Clean_Runbook(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Scan(representativeRunbook)
	}
}

func BenchmarkScan_Clean_Large(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Scan(representativeLargePayload)
	}
}

func BenchmarkScan_WithSecret_Runbook(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Scan(representativePayloadWithSecret)
	}
}

func BenchmarkWrapper_GenerateContent_Clean(b *testing.B) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)
	msgs := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, representativeRunbook[:512]),
		llms.TextParts(llms.ChatMessageTypeHuman, "Diagnose the issue."),
	}
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = wrapped.GenerateContent(ctx, msgs)
	}
}

func BenchmarkWrapper_GenerateContent_Disabled_IsZeroOverhead(b *testing.B) {
	inner := &fakeModel{respText: "ok"}
	// enabled=false should return inner unchanged — verify the bench shows
	// no overhead vs. calling inner directly.
	wrapped := WrapModel(inner, "openai", "gpt-4o", false, ModeEnforce)
	msgs := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Diagnose the issue."),
	}
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = wrapped.GenerateContent(ctx, msgs)
	}
}
