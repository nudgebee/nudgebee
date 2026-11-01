# OpenObserve alert validation

Validated on 2026-11-01 against OpenObserve v0.80.2 using the official image
`public.ecr.aws/zinclabs/openobserve:v0.80.2`
(image ID `sha256:8c12855e86d06e93c8d0f959f2bea6153a5011bb9cd2d0bbe1e6230957b95b9f`).
The disposable local server used synthetic `logs` and `up` streams and an explicit
`pr864_destination`; it had no connection to a production Nudgebee account.

From `api-server/services`, with credentials supplied through environment variables:

```sh
OPENOBSERVE_ALERT_TEST_URL=http://127.0.0.1:53326 \
OPENOBSERVE_ALERT_TEST_USER="$TEST_USER" \
OPENOBSERVE_ALERT_TEST_PASSWORD="$TEST_PASSWORD" \
GOMAXPROCS=2 GOMEMLIMIT=1GiB go test -p=1 ./observability/alertrule -run OpenObserve -count=1 -v
```

`TestOpenObserveAlertLive/sql` and `/promql` passed actual source create, update,
and delete calls. Only integration credential lookup used sqlmock. The entire
OpenObserve-focused test selection passed. A separate API readback verified the
updated period and configured destination. Names containing hyphens were accepted;
names containing spaces were rejected by this provider version.

Payload tests cover explicit destination validation, SQL row-count threshold 5,
explicit trigger threshold override, and PromQL ABOVE/BELOW comparisons with an
independent matched-series gate of count >= 1. HTTP tests cover all three methods,
authentication and provider errors. Integration lookup tests cover account/tenant
scoping, disabled integrations, multiple active integrations and configuration
failures. The generic flow-source package compiles after removing the redundant
OpenObserve flow source. Atlas v1.3.0 migration validation passed.

That initial CRUD test used disabled rules. The live test skips unless its three
environment variables are set; provider streams, template and
`pr864_destination` must already exist on the disposable server.

## Scheduled evaluation and HTTP delivery

A separate disposable run exercised enabled rules created, updated and deleted
through the actual `OpenObserveAlertRuleSource` methods at commit `67aa83fd8`.
Only the integration credential database lookup used sqlmock. Each rule used a
two-minute lookback, one-minute frequency and zero-minute silence.

| Case | Synthetic input | Notifications during 110 seconds |
| --- | --- | --- |
| SQL matching-row count > 1 | Two rows with `value = 1` | 3 |
| SQL matching-row count > 5 | The same two rows | 0 |
| PromQL `up` BELOW 5, matched-series count >= 1 | One series with value 1 | 3 |
| PromQL `up` ABOVE 5 | The same series with value 1 | 0 |

The receiver checked rule identity, organization, matching count, operator and
threshold in the actual HTTP payloads. The firing SQL rule was then updated to
`WHERE value > 99`, and the firing PromQL rule to BELOW 0. Two fresh log rows were
ingested and a search confirmed they still matched the original SQL predicate;
metric samples continued to refresh. Neither updated rule delivered during the
next 75 seconds. All four deletes passed, and each provider readback returned 404.

The receiver ran on loopback in the disposable provider's network namespace.
`ZO_SSRF_ALLOW_LOOPBACK=true` was enabled only for that server. The initial attempt
to deliver to `host.docker.internal` was rejected by the provider's SSRF guard;
the guard was not changed. The container and credential file were removed after
the run. Sanitized results and two representative payloads are recorded in
[`openobserve-alert-scheduled-evidence.json`](openobserve-alert-scheduled-evidence.json).

To reproduce, configure a disposable server, a raw HTTP template containing the
fields shown in that evidence, and a receiver destination. Ingest two log rows and
one fresh `up = 1` metric series. Create the four enabled rules in the table with
the stated lookback/frequency, observe notifications, apply the nonmatching
updates, ingest fresh data, observe again, then delete and read back each rule.

## Captured delivery replay through Nudgebee

The two distinct captured payloads were separately POSTed to the actual
`genericWebhookHandler("openobserve")` with a disposable PostgreSQL 16 database
using the existing webhook scenario schema. Both requests returned 200 and
persisted raw and parsed delivery rows. Assertions checked `openobserve_alert`,
processed status, the expected tenant/account IDs and the original alert names.
Missing and wrong tokens returned 400 and created no extra rows.

The broker port was deliberately closed, exercising the handler's synchronous
fallback. The minimal fixture lacks `event_rules`, `events`, `feature_flag` and
inventory tables, so downstream enrichment/event investigation logged those
expected missing-schema errors. Their success is not inferred from delivery
storage. The disposable database and its credential file were removed.

These are two connected evidence sets: provider scheduling to an HTTP receiver,
then captured-payload replay through Nudgebee's real handler/parser/storage. They
are not an uninterrupted provider-to-Nudgebee live chain and do not establish the
complete deployed incident/investigation pipeline. That remaining check needs a
fully migrated app database with a test tenant/account/integration, running
webhook workers/broker and the configured investigation services/model endpoint.
