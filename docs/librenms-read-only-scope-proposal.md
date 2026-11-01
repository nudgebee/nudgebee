# Network-device discovery through forager

**Status: draft for maintainer review.** This revises the proposal for
[issue #861](https://github.com/nudgebee/nudgebee/issues/861) and the
[network-discovery assessment](https://github.com/nudgebee/nudgebee/pull/860).
It proposes extensions to the existing discovery pipeline; it does not add a
standalone api-server LibreNMS connector.

## Start from the existing discovery pipeline

Forager runs inside the customer network and registers a discovery datasource.
The api-server orchestration already provides:

| Concern | Existing path |
|---|---|
| Network scope | `DiscoveryLabels.AllowedCIDRs` and `discovery_sweep` in [cron.go](../api-server/services/vmpackage/cron.go) |
| Inventory | `discovery_inventory` with versioned SSH content packs in [discovery.go](../api-server/services/vmpackage/discovery.go) |
| Scheduling | Cron sweep and queue-consumer orchestration in [cron.go](../api-server/services/vmpackage/cron.go) |
| Account scope | The agent's `own` account and explicit `discovery_target` link in [admin.go](../api-server/services/vmpackage/admin.go) |
| Resource matching | Existing-resource matching and the `vm-<ip>` fallback in [resource_match.go](../api-server/services/vmpackage/resource_match.go) |

These mechanisms answer the reachability, scheduling, and account-mapping
questions raised in the earlier memo. Credentials remain in the customer
network. Network devices should enter the same discovery → resource-match →
knowledge-graph path as SSH-discovered hosts, with the required network-device
schema extensions reviewed explicitly.

## Native SNMP inventory first

Propose SNMP as another forager inventory method beside SSH. The agent-side
implementation would use GoSNMP, with OID/vendor normalization supplied through
versioned content packs. The forager code lives in the agent repository, so this
requires coordinated agent and content-pack work.

Reuse the existing datasource, allowed CIDRs, scheduling, and target-account
association. Preserve bounded request/retry behavior, and never send SNMP
credentials to api-server or persist them in inventory output. Adding an SNMP
method must not reinterpret an existing SSH inventory result or assign a
switch/router the current VM fallback identity without an agreed mapping.

## Optional LibreNMS source

LibreNMS may be an optional read-only source accessed by forager inside the same
network. It would feed normalized device/interface inventory into the same
pipeline, rather than introduce a second inventory, api-server connector, or
account-identity model.

Start with API v0 device/interface reads, subject to a live compatibility check
against a maintainer-approved LibreNMS release. Do not add alert writes,
acknowledgements, configuration changes, raw metric ingestion, or topology
edges to that first source. Alert ingestion, if later required, needs a separate
mapping and lifecycle decision rather than treating an alert as device inventory.

## Decisions still required

- Graph node types and relationships for switches, routers, and interfaces.
- Stable identifiers and reconciliation rules across SNMP, SSH, LibreNMS, and
  already-known cloud resources; the VM matching fallback is not a network-device
  identity contract.
- Agent inventory schema/content-pack contract and compatibility with older agents.
- Supported SNMP security modes, vendor/OID coverage, request budgets, and the
  supported LibreNMS release range.
- Stale-record and partial-failure behavior, including what evidence is required
  before an absent inventory item may be retired.

## Acceptance checklist

- [ ] Maintainers approve the agent/content-pack scope and network-device schema.
- [ ] A device discovered through either source follows the existing target-account
      association and resource-match path, retaining tenant/account provenance.
- [ ] Repeated reads retain stable identities; overlapping sources do not create
      competing devices, and separate tenants/accounts remain isolated.
- [ ] Credentials stay in-network and no source performs configuration/alert writes.
- [ ] A bounded live agent run observes the expected device/interface inventory;
      failure and partial-result checks preserve previously known resources.

