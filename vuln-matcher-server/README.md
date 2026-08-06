# vuln-matcher-server

Matches package inventories against vulnerability data.

Give it an operating system and a list of packages; it returns the
vulnerabilities affecting them and the version that fixes each one. It holds no
database credentials, knows nothing about tenants, and schedules nothing —
services-server owns all of that.

Design and the evidence behind its decisions:
[`docs/vm-vuln-matcher-service-design.md`](../docs/vm-vuln-matcher-service-design.md).

## Running it locally

You need a vulnerability database on disk. The quickest way to get one is to let
the service download it:

```bash
mkdir -p ~/.cache/nudgebee/vuln-db
VULN_DB_ROOT=~/.cache/nudgebee/vuln-db VULN_DB_UPDATE=true make run
```

First start downloads ~139 MB and unpacks it to ~1.8 GB, so it takes a minute.
After that, run with `VULN_DB_UPDATE=false` and it loads in about a second.

If you already have a grype database cache, point `VULN_DB_ROOT` at the
directory containing the `6/` schema directory and skip the download.

```bash
curl localhost:8080/readyz
curl localhost:8080/v1/capabilities | jq '.supported[].family'
```

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `VULN_DB_ROOT` | `/var/lib/vuln-matcher/db` | where the database lives on disk |
| `VULN_DB_UPDATE` | `true` | fetch/refresh on start. Set `false` for air-gapped installs, where the database is placed on disk out of band |
| `VULN_DB_UPDATE_URL` | unset | our own mirror. Customers reach the database through one endpoint we host, never the upstream publisher |
| `VULN_DB_MAX_AGE` | `168h` | how stale the database may be before `/readyz` fails |
| `LISTEN_ADDR` | `:8080` | listen address |

## Asking it something

Send one operating system and a **deduplicated** package set. Callers collapse a
fleet down to distinct package tuples, ask once, and fan the answers back out to
hosts — a 400-machine fleet is a handful of requests, not 400.

```bash
curl -sS localhost:8080/v1/match -H 'Content-Type: application/json' -d '{
  "os": {"family": "redhat", "version": "9"},
  "packages": [{
    "key": "t1", "name": "openssl-libs", "type": "rpm",
    "version": "3.0.7-24.el9", "arch": "x86_64", "epoch": 1,
    "source_name": "openssl"
  }]
}' | jq '.findings | length'
```

`key` is yours; it comes back on every finding so you can map results to hosts.

## Why it refuses things

The failure that matters here is not an error — it is a confident "no
vulnerabilities found" on a host that has plenty. Measurement drove these:
omitting the source package on one real host returned **0 findings instead of
51**, and scanning a distro the database has no data for returns nothing at all
while looking perfectly healthy.

So bad input is rejected with `422` rather than answered with an empty list:

| Code | Cause |
|---|---|
| `missing_source_name` | rpm/deb package without `source_name`. Advisories are published against the source package, not the binary one |
| `unsupported_os` | the loaded database has no data for that OS. Check `/v1/capabilities` |
| `unknown_os_family` | no OS family given |
| `invalid_package` | missing key/name/version, or an unsupported package type |

A package set that legitimately matches nothing still returns `200`, but with
`suspect_zero: true`. Callers must alarm on that rather than record a clean host.

`/v1/capabilities` reports what the loaded database actually covers, read from
the database itself. Use it as an allowlist before scanning.

## Health

- `/healthz` — liveness. Deliberately ignores the database, so a slow first
  download cannot restart the pod in a loop.
- `/readyz` — readiness. Fails while the database is unloaded **or** older than
  `VULN_DB_MAX_AGE`, so an air-gapped install that quietly stopped receiving
  updates cannot keep looking healthy.

## Tests

```bash
make test
```

The engine tests need a real database and skip without one:

```bash
VULN_TEST_DB_ROOT=~/.cache/nudgebee/vuln-db go test ./internal/engine/... -v
```

They pin the three behaviours the design rests on: source-package resolution,
backported vendor fixes clearing a finding, and module streams scoping results
to what the host actually runs.

## A note on grype

Grype lives behind `internal/engine` and nothing else imports it. It is
semver-v0 and breaks its API on minor releases, so it is pinned and confined:
an upgrade that changes it fails to compile, which is the point. Wrapping its
CLI instead would let the same change pass silently, because its scan output is
not versioned.

The same boundary is what lets Windows (which needs Microsoft's data and build
comparison, not package matching) plug in later behind the same API without
callers changing.
