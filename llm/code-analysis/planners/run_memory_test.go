package planners

import "testing"

func TestRunMemory_StoreAndLookup(t *testing.T) {
	m := NewRunMemory()

	if _, _, ok := m.LookupRead("h1"); ok {
		t.Fatal("expected miss on empty cache")
	}

	m.StoreRead("h1", 3, "file contents")
	obs, step, ok := m.LookupRead("h1")
	if !ok || obs != "file contents" || step != 3 {
		t.Fatalf("expected hit (file contents, step 3), got obs=%q step=%d ok=%v", obs, step, ok)
	}
}

func TestRunMemory_StoreKeepsFirst(t *testing.T) {
	m := NewRunMemory()
	m.StoreRead("h1", 3, "first")
	m.StoreRead("h1", 9, "second") // must not overwrite

	obs, step, ok := m.LookupRead("h1")
	if !ok || obs != "first" || step != 3 {
		t.Fatalf("expected first observation kept, got obs=%q step=%d", obs, step)
	}
}

func TestRunMemory_InvalidateOnMutation(t *testing.T) {
	m := NewRunMemory()
	m.StoreRead("h1", 1, "stale after edit")

	if _, _, ok := m.LookupRead("h1"); !ok {
		t.Fatal("expected hit before invalidation")
	}

	m.Invalidate() // a mutating tool changed the tree

	if _, _, ok := m.LookupRead("h1"); ok {
		t.Fatal("expected miss after invalidation (tree changed)")
	}

	// A fresh read after the mutation is cached at the new version and hits again.
	m.StoreRead("h1", 5, "fresh after edit")
	obs, step, ok := m.LookupRead("h1")
	if !ok || obs != "fresh after edit" || step != 5 {
		t.Fatalf("expected fresh cached read, got obs=%q step=%d ok=%v", obs, step, ok)
	}
}

func TestRunMemory_RecordFixAttempt(t *testing.T) {
	m := NewRunMemory()

	if prior := m.RecordFixAttempt("", 1); prior != 0 {
		t.Fatalf("empty diff must be ignored, got prior=%d", prior)
	}
	if prior := m.RecordFixAttempt("diffA", 1); prior != 0 {
		t.Fatalf("first occurrence of a diff is new, got prior=%d", prior)
	}
	if prior := m.RecordFixAttempt("diffB", 2); prior != 0 {
		t.Fatalf("a different diff is new, got prior=%d", prior)
	}
	if prior := m.RecordFixAttempt("diffA", 3); prior != 1 {
		t.Fatalf("repeat of diffA must report first attempt (1), got prior=%d", prior)
	}

	var nilM *RunMemory
	if prior := nilM.RecordFixAttempt("diffA", 1); prior != 0 {
		t.Fatalf("nil receiver must return 0, got prior=%d", prior)
	}
}

func TestRunMemory_EditedFiles(t *testing.T) {
	m := NewRunMemory()
	if len(m.EditedFiles()) != 0 {
		t.Fatal("expected no edited files initially")
	}
	m.RecordEditedFile("a.py")
	m.RecordEditedFile("a.py") // dedup
	m.RecordEditedFile("b.go")
	m.RecordEditedFile("") // ignored

	got := map[string]bool{}
	for _, f := range m.EditedFiles() {
		got[f] = true
	}
	if len(got) != 2 || !got["a.py"] || !got["b.go"] {
		t.Fatalf("expected {a.py, b.go}, got %v", got)
	}

	var nilM *RunMemory
	nilM.RecordEditedFile("x") // no panic
	if nilM.EditedFiles() != nil {
		t.Fatal("nil receiver must return nil")
	}
}

func TestRunMemory_NilReceiverSafe(t *testing.T) {
	var m *RunMemory // legacy callers pass nil
	if _, _, ok := m.LookupRead("h1"); ok {
		t.Fatal("nil RunMemory must report a miss")
	}
	m.StoreRead("h1", 1, "x") // must not panic
	m.Invalidate()            // must not panic
}
