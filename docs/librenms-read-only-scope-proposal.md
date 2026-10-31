# LibreNMS Read-Only Scope Proposal

**Status: draft proposal for maintainer review.** This memo narrows the first
release described in [PR #860](https://github.com/nudgebee/nudgebee/pull/860)
and [issue #861](https://github.com/nudgebee/nudgebee/issues/861); it is not an
approved design.

## Proposed first release

Import only **devices, interfaces, and active alerts** from LibreNMS. Keep the
connector read-only: it may list and refresh those records, but it must not
acknowledge, close, suppress, create, or otherwise mutate LibreNMS alerts or
configuration. Metrics, topology edges, and native SNMP polling are not part
of this release.

The evidence supports **LibreNMS API v0** for the first implementation: the
official [API documentation](https://docs.librenms.org/API/) documents v0 and
the [v1 documentation](https://docs.librenms.org/API/v1/) labels v1 beta and
disabled by default. This is a version recommendation, not a claim about the
versions Nudgebee will support; the owner must choose the supported LibreNMS
version range after a live compatibility check.

## Identity and provenance boundary

The connector should follow the existing account-scoped integration and graph
patterns rather than introduce a parallel tenant model:

- Resolve credentials through the existing integration configuration path,
  which requires an account mapping for integrations that are not explicitly
  tenant-scoped: [`integration_config.go`](../api-server/services/integrations/core/integration_config.go)
  and [`interface.go`](../api-server/services/integrations/core/interface.go).
- Stamp every node and edge with the originating `TenantID`, the selected
  `CloudAccountID`, and a distinct source name. Those fields are part of the
  graph model in [`types.go`](../api-server/services/knowledge_graph/core/types.go).
- Use the registered-source path in [`sources/registry.go`](../api-server/services/knowledge_graph/sources/registry.go)
  and the six-part graph identity convention in
  [`unique_key_builder.go`](../api-server/services/knowledge_graph/core/unique_key_builder.go):
  provider, account, location, node type, hierarchy, and name.

**Proposal:** a LibreNMS instance must have an explicit stable source-instance
identity within the selected tenant/account scope, and device/interface/alert
keys must use stable identifiers proven by the target deployment. Do not use a
display name alone when it can collide. The repository does not establish a
LibreNMS instance field, endpoint field, or multi-account mapping rule, so
those remain implementation decisions; fail closed rather than merge records
when the required identity or scope is absent.

## Owner decisions still required

- Whether the Nudgebee service can reach each customer’s LibreNMS HTTPS endpoint
  directly, or whether an in-network agent/proxy is required.
- The supported LibreNMS version range and the deployment used for a live API
  compatibility proof.
- Refresh cadence, stale-record handling, and retention/tombstone policy.
- Whether one LibreNMS integration may map to multiple Nudgebee accounts, and
  how its source-instance identity is stored.

## Explicit exclusions

- **Topology:** defer neighbor edges until the deployed API exposes the needed
  data with stable identifiers; the API overview does not document a dedicated
  neighbor endpoint. The discovery modules’ LLDP/CDP/FDP capability is not
  itself proof of a stable API contract:
  [discovery support](https://docs.librenms.org/Support/Discovery%20Support/).
- **Native SNMP:** defer until deployment reachability, credential handling,
  polling/retry limits, and OID/vendor normalization are proven.
- **Alert writes:** defer until an explicit write authorization and lifecycle
  contract exists; the first release remains read-only.

## Acceptance checklist

- [ ] A maintainer approves the supported LibreNMS version range and a live
      API v0 compatibility proof covers devices, interfaces, and active alerts.
- [ ] A configured integration is resolved only within its tenant and selected
      account scope; missing or ambiguous identity fails without importing data.
- [ ] Repeated reads produce stable graph identities and retain source,
      tenant, and account provenance without cross-tenant mixing.
- [ ] The connector performs no alert or LibreNMS configuration writes.
- [ ] Reachability, refresh cadence, stale handling, and retention have explicit
      owner decisions before implementation starts.
