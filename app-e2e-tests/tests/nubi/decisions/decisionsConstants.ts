// Not for OSS
// Shared timeouts for the Decisions suite, named for what is being waited on rather
// than for their length, so the spec and the helper cannot drift apart on the same
// wait. Same pattern as tests/admin/UsageLimits/usageLimitsConstants.ts.

// Whole-case budget. Every case pays a full login plus a Nubi panel open before it
// reaches the tab, which is why this is well above the config's 120s dev default.
export const CASE_TIMEOUT = 150000;

// The list settling after a fetch — the tab's first render, and every refetch a
// scope or history change triggers. The slowest thing this suite waits on.
export const LOAD_TIMEOUT = 30000;

// A tab or header appearing or detaching after a click. Slower than a control
// because b-Cortex swaps the whole tab body.
export const MOUNT_TIMEOUT = 20000;

// One control committing its own state — aria-checked on a scope radio, the history
// button's label, a chip count. These follow a React commit, not a round trip.
export const CONTROL_TIMEOUT = 15000;
