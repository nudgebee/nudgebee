# Optimization trigger browser validation

On 2026-11-01, headless Chrome exercised the actual `TriggerConfigSidebar`,
optimization-category helpers, `FormField` and design-system `Select` from
implementation commit `499e0ddba`. Playwright CLI drove the visible controls.

The isolated browser fixture used synthetic account options and a loopback HTTP
transport. The API method `getDistinctOptimizationRules` and `gqlStringify` were
extracted unchanged from the source; recorded hashes identify those inputs.
Unrelated API modules, icons, schedule builder and simulator were excluded.

| Interaction | Observed result |
| --- | --- |
| Switch from saved Kubernetes account/rule to AWS before its request completes | Old rule selection clears; outgoing query contains the AWS account predicate. |
| Release AWS response, then the older Kubernetes response | Rule picker offers only `aws_idle_instance`; the stale response does not restore Kubernetes rules. |
| Select the AWS rule and Save | Emitted config contains AWS account, RightSizing category and AWS rule. |
| Open an existing cloud trigger with saved Spot category | Its saved category stays visible and editable. |
| Change that saved category to Right Sizing, then reopen the category picker | Old rule clears; only the three cloud-compatible categories remain. |
| Clear the optional account scope | Query omits the account predicate and the rule picker offers both synthetic accounts' RightSizing rules. |

[Recorded assertions, requests and source hashes](optimization-trigger-browser-evidence.json)
preserve the fixture observations. The existing rendered-sidebar regression is
`app/src/components/workflow/components/__tests__/TriggerConfigSidebar.optimization.test.tsx`.

For verification in a deployed application, open an optimization trigger with
two accounts containing distinct recommendations. Throttle the first account's
rule request, switch accounts, and release responses in reverse order. Check the
rule list and saved config, then repeat the saved-category and cleared-scope
cases above.

This is browser component/transport coverage. It does not exercise NextAuth,
the application RPC gateway, server tenant authorization, database-backed
recommendations, a persisted workflow save or an actual workflow run. Those need
an authenticated test deployment and seeded accounts. Initial setup-only
branding/favicon 404s and fixture parent-state resets were corrected before the
final five-group assertion run; no product failure is inferred from those.
