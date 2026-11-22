package alertrule

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SolarWinds /v1/tfproxy allowlist keys on the exact bytes of the GraphQL
// document. That makes these documents data, not source: reformatting one is
// indistinguishable from a tidy-up in review, and the first symptom in
// production is a 403 on a customer's request.
//
// CI cannot verify the allowlist itself — it is a server-side property, and the
// only honest check is a live call (see the liveSolarWinds build tag). What CI
// can do is refuse to let the bytes change silently, which is what these tests
// are for. The digest is the oracle; the rest exist to make the failure message
// say what actually went wrong.

func TestSolarWindsDocumentsMatchTheirDigests(t *testing.T) {
	require.NotEmpty(t, swoAlertDocuments)

	for operationName, want := range swoDocumentDigests {
		t.Run(operationName, func(t *testing.T) {
			_, held := swoAlertDocuments[operationName]
			require.True(t, held, "a digest is pinned for %q but no document is held for it", operationName)

			assert.Equal(t, want, swoDocumentDigest(operationName),
				"The SolarWinds /v1/tfproxy allowlist matches this GraphQL document "+
					"byte for byte, framing included, so it has to be sent exactly as the "+
					"SolarWinds Terraform provider emits it.\n\n"+
					"This document no longer hashes to the pinned value, which means it was "+
					"reformatted, re-indented, re-wrapped, or had its framing newlines "+
					"changed. Sending it now returns 403 \"GraphQL request is not allowed.\"\n\n"+
					"Revert the edit. If you are deliberately re-capturing the document from a "+
					"newer provider build, update the digest in the same change and prove it "+
					"with a liveSolarWinds run — do not update the digest to make this pass.")
		})
	}
}

// Every held document must be pinned. A new document added without a digest
// would otherwise be free to drift.
func TestSolarWindsEveryDocumentIsPinned(t *testing.T) {
	for operationName := range swoAlertDocuments {
		_, pinned := swoDocumentDigests[operationName]
		assert.True(t, pinned, "document %q has no pinned digest", operationName)
	}
	assert.Len(t, swoDocumentDigests, len(swoAlertDocuments),
		"the document and digest maps must describe the same set of operations")
}

// The framing is the part most easily lost: it is whitespace at the very edges
// of a string, which is exactly what editors and formatters normalise away.
func TestSolarWindsDocumentsCarryTheirFramingNewlines(t *testing.T) {
	for operationName, document := range swoAlertDocuments {
		t.Run(operationName, func(t *testing.T) {
			assert.True(t, strings.HasPrefix(document, "\n"),
				"a leading newline is part of the allowlisted bytes")
			assert.True(t, strings.HasSuffix(document, "\n"),
				"a trailing newline is part of the allowlisted bytes")
			assert.NotContains(t, document, "\r",
				"CRLF line endings would change every line's bytes")

			// Exactly one newline at each edge: swoFrame adds one, and the body
			// must not have gained or lost its own.
			body := strings.TrimPrefix(strings.TrimSuffix(document, "\n"), "\n")
			assert.Equal(t, len(body)+2, len(document),
				"the document must be the body plus exactly one newline at each end")
			assert.False(t, strings.HasPrefix(body, "\n"), "body gained a leading newline")
			assert.False(t, strings.HasSuffix(body, "\n"), "body gained a trailing newline")
		})
	}
}

// A copy-paste that swaps two entries in the map would otherwise send a valid,
// allowlisted document for the wrong operation — which fails as a confusing
// GraphQL error rather than as an obvious mismatch.
func TestSolarWindsDocumentsDeclareTheirOwnOperationName(t *testing.T) {
	for operationName, document := range swoAlertDocuments {
		t.Run(operationName, func(t *testing.T) {
			first := strings.SplitN(strings.TrimLeft(document, "\n"), "\n", 2)[0]
			assert.Contains(t, first, operationName,
				"the document's own header must name the operation it is mapped under; got %q", first)
		})
	}
}

// The provider emits tab-indented documents. This is not a style preference —
// it records what the allowlist accepted, so that a well-meant conversion to
// spaces is recognisable as the cause rather than a mystery 403.
func TestSolarWindsDocumentsAreTabIndented(t *testing.T) {
	for operationName, document := range swoAlertDocuments {
		t.Run(operationName, func(t *testing.T) {
			assert.Contains(t, document, "\n\t", "the document is tab indented as captured")
			assert.NotContains(t, document, "\n  ", "space indentation means the bytes were rewritten")
		})
	}
}

// The read document's selection set is fixed by the allowlist, so it is also
// the ceiling on what a future read-merge-write can preserve. These fields are
// what make a non-destructive update possible at all: an update replaces the
// whole AlertDefinitionInput, so anything not readable here cannot be carried
// over and would be silently reset.
func TestSolarWindsReadSelectionSetCoversTheFieldsAnUpdateMustPreserve(t *testing.T) {
	document := swoAlertDocuments[swoOpGetAlertDefinitionByID]
	require.NotEmpty(t, document)

	for _, field := range []string{
		"actions",               // the notification targets themselves
		"configurationIds",      // which notification configurations are attached
		"resendIntervalSeconds", // how often an unresolved alert re-notifies
		"triggerResetActions",   // whether a recovery notifies
		"noDataResetSeconds",
		"runbookLink",
		"severity",
		"enabled",
		"targetEntityTypes",
		"triggerDelaySeconds",
		"flatCondition", // the condition itself, as a node graph
	} {
		assert.Contains(t, document, field,
			"%q is part of AlertDefinitionInput, so an update that cannot read it would reset it", field)
	}
}
