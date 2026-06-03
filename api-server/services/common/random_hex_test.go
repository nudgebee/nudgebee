package common

import (
	"encoding/hex"
	"testing"
)

func TestGenerateRandomHexString(t *testing.T) {
	n := 32
	s, err := GenerateRandomHexString(n)
	if err != nil {
		t.Fatalf("GenerateRandomHexString failed: %v", err)
	}
	if len(s) != n*2 {
		t.Errorf("Expected length %d, got %d", n*2, len(s))
	}
	decoded, err := hex.DecodeString(s)
	if err != nil {
		t.Errorf("Failed to decode hex string: %v", err)
	}
	if len(decoded) != n {
		t.Errorf("Expected decoded length %d, got %d", n, len(decoded))
	}
}
