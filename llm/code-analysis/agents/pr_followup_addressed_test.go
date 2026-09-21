package agents

import (
	"testing"
	"time"
)

// TestAddressedCommentKeys pins the lookup the gatherers skip against (#36865).
// The key shape must match answeredCommentKey exactly, or the DB record and the
// GitHub-derived marker set would live in separate namespaces and the fallback
// would silently never fire.
func TestAddressedCommentKeys(t *testing.T) {
	t.Run("empty record yields a nil map", func(t *testing.T) {
		// Nil (not empty-non-nil) matters only as documentation of intent: a nil
		// map reads as all-false, so every gatherer's skip check is a no-op and
		// behaviour is exactly what it was before this existed.
		if got := addressedCommentKeys(nil); got != nil {
			t.Fatalf("expected nil for an empty record, got %v", got)
		}
		if got := addressedCommentKeys([]AddressedComment{}); got != nil {
			t.Fatalf("expected nil for an empty slice, got %v", got)
		}
	})

	t.Run("keys match answeredCommentKey", func(t *testing.T) {
		keys := addressedCommentKeys([]AddressedComment{
			{Source: "inline", CommentID: 11, Action: "fixed", AddressedAt: time.Now()},
			{Source: "issue_comment", CommentID: 22, Action: "wont_fix", AddressedAt: time.Now()},
		})
		for _, want := range []string{
			answeredCommentKey("inline", 11),
			answeredCommentKey("issue_comment", 22),
		} {
			if !keys[want] {
				t.Errorf("missing key %q; have %v", want, keys)
			}
		}
	})

	t.Run("same id in different sources are distinct keys", func(t *testing.T) {
		// GitHub issue comments and review submissions are separate id spaces,
		// so collapsing on the number alone would suppress an unrelated comment.
		keys := addressedCommentKeys([]AddressedComment{
			{Source: "inline", CommentID: 42},
			{Source: "issue_comment", CommentID: 42},
		})
		if len(keys) != 2 {
			t.Fatalf("expected 2 distinct keys, got %d: %v", len(keys), keys)
		}
	})

	t.Run("half-identified entries are dropped", func(t *testing.T) {
		keys := addressedCommentKeys([]AddressedComment{
			{Source: "", CommentID: 1},
			{Source: "inline", CommentID: 0},
			{Source: "inline", CommentID: 5},
		})
		if len(keys) != 1 || !keys[answeredCommentKey("inline", 5)] {
			t.Fatalf("expected only the fully-identified entry, got %v", keys)
		}
	})
}

// TestAddressedRecordIsFallbackNotAuthority documents the ordering that makes
// this safe (#36865), and guards it against a future refactor.
//
// The DB record only ADDS skips. It never resurrects a comment GitHub says is
// open, and — critically — it must never be consulted INSTEAD of GitHub's
// resolved-thread state. If a human un-resolves a thread, GitHub reports it
// open again and the comment must come back as pending; a DB-authoritative
// design would suppress it forever, which is the #36625 silence failure mode.
func TestAddressedRecordIsFallbackNotAuthority(t *testing.T) {
	const commentID = int64(99)

	record := addressedCommentKeys([]AddressedComment{
		{Source: "inline", CommentID: commentID, Action: "fixed", AddressedAt: time.Now()},
	})
	none := map[int64]bool{}
	hit := map[int64]bool{commentID: true}

	cases := []struct {
		name              string
		replied, resolved map[int64]bool
		db                map[string]bool
		want              bool
	}{
		// The case the feature exists for: the markers scrolled past the
		// per_page=100 window so GitHub tells us nothing, but we know better.
		{"DB record alone is enough", none, none, record, true},
		// GitHub alone still works with no record at all — this is every PR
		// before the column existed, and must be unchanged.
		{"replied marker alone is enough", hit, none, nil, true},
		{"resolved thread alone is enough", none, hit, nil, true},
		// The invariant that keeps #36625 from coming back: a comment GitHub
		// reports as open, with no record of us answering it, stays pending.
		// A human un-resolving a thread lands exactly here.
		{"nothing known means pending", none, none, nil, false},
		// And an unrelated comment id in the record must not suppress this one.
		{"record for a different comment does not suppress", none, none,
			addressedCommentKeys([]AddressedComment{{Source: "inline", CommentID: 1}}), false},
		// Same number, different source — separate GitHub id spaces.
		{"record under a different source does not suppress", none, none,
			addressedCommentKeys([]AddressedComment{{Source: "issue_comment", CommentID: commentID}}), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := inlineCommentAddressed(commentID, c.replied, c.resolved, c.db); got != c.want {
				t.Fatalf("inlineCommentAddressed = %v, want %v", got, c.want)
			}
		})
	}
}

// TestAddressedCommentRoundTrip checks the wire shape survives the hop into
// api-server, which unmarshals these into its own struct by JSON tag.
func TestAddressedCommentRoundTrip(t *testing.T) {
	entry := AddressedComment{
		Source:      "review_body",
		CommentID:   1234567890,
		Action:      "acknowledged",
		AddressedAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC),
	}
	keys := addressedCommentKeys([]AddressedComment{entry})
	if !keys["review_body:1234567890"] {
		t.Fatalf("unexpected key shape: %v", keys)
	}
}
