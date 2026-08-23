package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestK8sNamespaceRegex(t *testing.T) {
	valid := []string{
		"default",
		"kube-system",
		"my-namespace",
		"prod",
		"a",
		"ns-123",
		"a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5abc", // 63 chars
	}
	for _, ns := range valid {
		assert.True(t, k8sNamespaceRe.MatchString(ns), "should accept valid namespace: %s", ns)
	}

	invalid := []string{
		"default; curl evil.com",
		"ns$(whoami)",
		"-leading-hyphen",
		"trailing-hyphen-",
		"UPPERCASE",
		"has space",
		"has.dot",
		"has_underscore",
		"a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5abcd", // 64 chars
		"",
	}
	for _, ns := range invalid {
		assert.False(t, k8sNamespaceRe.MatchString(ns), "should reject invalid namespace: %q", ns)
	}
}
