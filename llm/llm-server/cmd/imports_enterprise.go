//go:build enterprise

// This file is excluded from the default build and is only included when the
// `enterprise` build tag is set (`go build -tags enterprise ./...`). It holds
// blank imports that register optional extension packages on init().
//
// Two reasons to keep this isolated from main.go:
//
//  1. The default build (no tag) compiles without these imports, so it does
//     not require the imported packages to be present in the source tree.
//  2. main.go itself contains no reference to the extension paths, so a
//     sync-to-public-source-repo step does not need to know which lines to
//     strip — it just drops files whose name matches the convention.
//
// Add new extension registrations here, not in main.go.

package main

import _ "nudgebee/llm/ee/egressfilter" // registers the curated extension bundle
