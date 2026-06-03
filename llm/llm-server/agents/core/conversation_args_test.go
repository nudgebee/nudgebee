package core

import (
	"testing"

	"github.com/google/shlex"
)

func TestShlexSplitBasic(t *testing.T) {
	input := `/call foo bar baz`
	parts, err := shlex.Split(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 4 || parts[0] != "/call" || parts[1] != "foo" || parts[2] != "bar" || parts[3] != "baz" {
		t.Fatalf("unexpected split: %v", parts)
	}
}

func TestShlexSplitQuoted(t *testing.T) {
	input := `/call foo "arg with spaces" 'single quoted' simple`
	parts, err := shlex.Split(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 5 {
		t.Fatalf("unexpected split length: %v", parts)
	}
	if parts[2] != "arg with spaces" || parts[3] != "single quoted" || parts[4] != "simple" {
		t.Fatalf("unexpected parts: %v", parts)
	}
}

func TestShlexSplitEscapes(t *testing.T) {
	input := `/call foo "escaped \"quote\" inside" back\\slash`
	parts, err := shlex.Split(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("unexpected split length: %v", parts)
	}
	if parts[2] != `escaped "quote" inside` {
		t.Fatalf("unexpected escaped part: %v", parts)
	}
	if parts[3] != `back\slash` {
		t.Fatalf("unexpected backslash part: %v", parts)
	}
}

func TestShlexSplitMismatchedQuote(t *testing.T) {
	input := `/call foo "unterminated`
	_, err := shlex.Split(input)
	if err == nil {
		t.Fatalf("expected error for mismatched quote")
	}
}
