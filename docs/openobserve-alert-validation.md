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

Rules were disabled. This verifies provider CRUD compatibility, not scheduled
alert evaluation or actual notification delivery to Nudgebee. The live test skips
unless its three environment variables are set; provider streams, template and
`pr864_destination` must already exist on the disposable server.
