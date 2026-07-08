# sample_manual_dependencies.csv

End-to-end exerciser for the Manual Dependencies feature (NB-30989, Phase 1
backend + Phase 2 UI). Drop this file into the **Import CSV** dialog on the
Knowledge Graph → Manual Dependencies tab, or POST the body to
`kg_import_manual_dependencies` via curl.

The CSV uses real K8sService names that exist in the Nudgebee dev tenant
(`app-prod`, `auto-pilot-server`, `benchmark-server`, etc.) so the "should
resolve" / "should be ambiguous" assertions actually fire against live KG
data. The cross-stack rows use synthetic ARNs that won't resolve unless your
tenant has matching AWS resources ingested — they still verify the
ARN-bearing code paths.

## Expected outcomes

| Row | Category | Expected `resolution_status` | Why |
|-----|----------|------------------------------|-----|
| A1  | Happy path | `resolved` | `app-prod` exists exactly once in `nudgebee` on `k8s-prod`; dest the same |
| A2  | Happy path | `resolved` | Both sides fully specified, single match each |
| B1  | Ambiguity | `source_ambiguous` | `auto-pilot-server` exists on both `k8s-dev` and `k8s-prod`; no cluster on source |
| C1  | Ambiguity | `dest_ambiguous` | `benchmark-server` on both clusters; no cluster on dest |
| D1  | Both ambiguous | `source_ambiguous` (precedence) with **both** candidate arrays populated | Neither side specifies cluster — exercises the both-sides resolve panel in the UI |
| E1  | Unmatched | `source_unmatched` | Synthetic name doesn't exist in KG |
| E2  | Unmatched | `dest_unmatched` | Same for dest side |
| F1  | Cross-stack | `dest_unmatched` (typically) | Synthetic RDS ARN; verifies the AWS-side resolution path runs |
| F2  | Cross-stack | `dest_unmatched` (typically) | Lambda ARN + `PUBLISHES_TO`; verifies non-CALLS relationship type |
| F3  | Cross-stack | `dest_unmatched` (typically) | SQS ARN + `SUBSCRIBES_TO` |
| G1  | Relationship type | `dest_unmatched` (typically) | SNS topic ARN + `PUBLISHES_TO` |
| H1  | Default | `resolved` | Blank `relationship_type` column — parser must default to `CALLS` |
| I1  | Rejected | n/a — row appears in `rejected[]` | `OWNS` is not in the allowed enum |
| I2  | Rejected | n/a — `rejected[]` | `source_name` empty |
| I3  | Rejected | n/a — `rejected[]` | `dest_node_type` empty |
| I4  | Rejected | n/a — `rejected[]` | `source_node_type` empty |
| I5  | Rejected | n/a — `rejected[]` | `dest_name` empty |

## Response shape

`kg_import_manual_dependencies` returns:

```json
{
  "imported": [
    { "row_index": 0, "id": 101, "status": "resolved",         "match_count": 1 },
    { "row_index": 2, "id": 103, "status": "source_ambiguous", "match_count": 2 },
    ...
  ],
  "rejected": [
    { "row_index": 12, "error": "unsupported relationship_type \"OWNS\" (must be one of CALLS, PUBLISHES_TO, SUBSCRIBES_TO)" },
    { "row_index": 13, "error": "source_name is required" },
    ...
  ]
}
```

`row_index` is **zero-based and counts the data rows only** (the header row
is not counted).

## After import: UI walk-through

1. Open **Knowledge Graph** dialog → **Manual Dependencies** tab.
2. Expected table state:
   - 12 rows visible (`A1`–`H1`); 5 rejections surfaced in the import result viewer.
   - 2 rows show **Resolved** chips (`A1`, `A2`, `H1` — `H1` if its `app-prod` lookup succeeds).
   - 1 row shows **Source ambiguous** with a `[Resolve]` button (`B1`).
   - 1 row shows **Dest ambiguous** with a `[Resolve]` button (`C1`).
   - 1 row shows **Source ambiguous** but with **both** candidate arrays
     populated under the hood (`D1`) — clicking Resolve opens the
     two-section picker.
   - 4 rows show **Unmatched** chips (`E1`, `E2`, `F1`, `F2`, `F3`, `G1`).
3. Click the **Resolve** button on row `D1` → resolve view shows BOTH
   "Source candidates" and "Destination candidates" sections. The Resolve
   button is disabled until both sides have a pick.
4. Click **Status** filter → **Ambiguous** → table narrows to 3 rows
   (`B1`, `C1`, `D1`).
5. After resolving `D1`, the table refreshes and the chip turns green.
6. Click any **Resolved** row → no Resolve action (this is read-only).

## Curl equivalent

```bash
curl -sS "$API_SERVER/rpc/knowledge-graph" \
  -H 'Content-Type: application/json' \
  -H "X-ACTION-TOKEN: $ACTION_API_SERVER_TOKEN" \
  -H "x-tenant-id: $TENANT_ID" \
  -H "x-user-id: $USER_ID" \
  -d "$(jq -n --arg csv "$(cat sample_manual_dependencies.csv)" '{
    action: { name: "kg_import_manual_dependencies" },
    input:  { request: { csv: $csv } }
  }')" | jq
```

## Re-running after the first import

The unique dedupe index `idx_kg_manual_deps_dedupe` will reject duplicate
rows on a second import (same tenant, same endpoints, same
`relationship_type`). To re-run cleanly:

```bash
curl -sS "$API_SERVER/rpc/knowledge-graph" "${HEADERS[@]}" -d '{
  "action": { "name": "kg_delete_all_manual_dependencies" },
  "input":  { "request": {} }
}'
```

then re-import.
