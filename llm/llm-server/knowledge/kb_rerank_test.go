package knowledge

import "testing"

// parseKBJudgeVerdict decides, from free-form model output, whether the judge
// gave a verdict at all. Getting that wrong in either direction is costly: a
// missed verdict leaves noise in the prompt, and a fabricated one blanks the
// knowledge base.

func TestParseKBJudgeVerdictReadsTheChoice(t *testing.T) {
	// Candidates are numbered from 1 in the prompt; Keep is a slice index.
	v, ok := parseKBJudgeVerdict(`{"best": 3, "score": 0.9}`, 3)
	if !ok {
		t.Fatal("want a verdict")
	}
	if v.Keep != 2 || v.Score != 0.9 {
		t.Fatalf("want keep 2 score 0.9, got %+v", v)
	}
}

func TestParseKBJudgeVerdictZeroIsAKeepNoneVerdict(t *testing.T) {
	// "Nothing here is relevant" — the caller must suppress, not fall back.
	v, ok := parseKBJudgeVerdict(`{"best": 0, "score": 0}`, 3)
	if !ok {
		t.Fatal("zero is a keep-none verdict, not a parse failure")
	}
	if v.Keep != -1 {
		t.Fatalf("want keep none, got %+v", v)
	}
}

func TestParseKBJudgeVerdictUnparseableIsNotAVerdict(t *testing.T) {
	// The opposite: no usable output must leave the caller's order alone.
	if _, ok := parseKBJudgeVerdict("None of them are relevant, I'm afraid.", 3); ok {
		t.Fatal("prose with no verdict object must not be read as a verdict")
	}
	if _, ok := parseKBJudgeVerdict("", 3); ok {
		t.Fatal("an empty reply is not a verdict")
	}
}

func TestParseKBJudgeVerdictReadsThroughFencesAndProse(t *testing.T) {
	reply := "Looking at the candidates, [2] is closest.\n```json\n{\"best\": 2, \"score\": 0.75}\n```\n"
	v, ok := parseKBJudgeVerdict(reply, 4)
	if !ok || v.Keep != 1 {
		t.Fatalf("want keep 1 from the fenced object, got %+v ok=%v", v, ok)
	}
}

func TestParseKBJudgeVerdictTakesTheLastObject(t *testing.T) {
	// Models that reason in JSON before concluding must be read by the
	// conclusion, not the draft.
	v, ok := parseKBJudgeVerdict(`First {"best": 2, "score": 0.4} on reflection {"best": 0, "score": 0}`, 3)
	if !ok || v.Keep != -1 {
		t.Fatalf("want the last object to win, got %+v ok=%v", v, ok)
	}
}

func TestParseKBJudgeVerdictAcceptsQuotedAndFloatNumbers(t *testing.T) {
	for _, reply := range []string{`{"best": "2", "score": "0.5"}`, `{"best": 2.0, "score": 0.5}`} {
		v, ok := parseKBJudgeVerdict(reply, 3)
		if !ok || v.Keep != 1 || v.Score != 0.5 {
			t.Fatalf("want keep 1 score 0.5 from %s, got %+v ok=%v", reply, v, ok)
		}
	}
}

func TestParseKBJudgeVerdictRejectsAMissingChoice(t *testing.T) {
	// A pointer field exists so a missing "best" is detectable; without that
	// this would become a confident keep-none.
	if _, ok := parseKBJudgeVerdict(`{"score": 0.9}`, 3); ok {
		t.Fatal("an object with no choice is not a usable verdict")
	}
}

func TestParseKBJudgeVerdictToleratesAMissingScore(t *testing.T) {
	// The choice is the decision; the score only feeds the optional cutoff, so
	// its absence must not throw the verdict away.
	v, ok := parseKBJudgeVerdict(`{"best": 1}`, 2)
	if !ok || v.Keep != 0 || v.Score != 0 {
		t.Fatalf("want keep 0 score 0, got %+v ok=%v", v, ok)
	}
}

func TestParseKBJudgeVerdictClampsScores(t *testing.T) {
	high, _ := parseKBJudgeVerdict(`{"best": 1, "score": 4.2}`, 2)
	low, _ := parseKBJudgeVerdict(`{"best": 1, "score": -3}`, 2)
	if high.Score != 1 || low.Score != 0 {
		t.Fatalf("want scores clamped into 0-1, got %v and %v", high.Score, low.Score)
	}
}

func TestParseKBJudgeVerdictIgnoresAnOutOfRangeChoice(t *testing.T) {
	// A number nothing was offered at is a hallucination, not a decision, and
	// must not suppress the candidates.
	if _, ok := parseKBJudgeVerdict(`{"best": 50, "score": 0.9}`, 2); ok {
		t.Fatal("a choice outside the candidate range must not count as a verdict")
	}
	if _, ok := parseKBJudgeVerdict(`{"best": -1, "score": 0.9}`, 2); ok {
		t.Fatal("a negative choice must not count as a verdict")
	}
}
