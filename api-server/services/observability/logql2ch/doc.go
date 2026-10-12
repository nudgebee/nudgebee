// Package logql2ch translates LogQL into one ClickHouse SELECT over the
// otel_logs table, for the bundled log store (nudgebee-enterprise#40143).
//
// Translate takes LogQL (Code mode, the workflow builder); WhereCond takes a
// canonical where-clause (Builder mode, nubi, event rules) without rendering
// it as LogQL first. Both share one SQL renderer, accept only the labels in
// the alias map, and refuse any LogQL they cannot answer exactly, by name.
// The package does no I/O: callers run the SQL (through the agent's relay).
//
// testdata/corpus.yaml is the contract: every LogQL form Nudgebee emits, the
// SQL conditions each supported one becomes, and the exact error for each
// refused one. TestTranslate_Corpus checks it.
package logql2ch
