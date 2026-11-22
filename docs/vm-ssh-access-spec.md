# Spec: SSH access to discovered VMs (Nubi, runbooks, playbooks)

Status: draft · Repos: `forager`, `nudgebee`

## Problem

Forager discovers VMs and SSHes into them for inventory, but that access is not
exposed to the platform. Only a `discovery` integration gets registered, so
Nubi, runbooks, and playbooks cannot run commands on discovered VMs.

Typical state: a connected forager with one `discovery-proxy` datasource, no
`ssh` integration, and `vm_package` rows for most in-scope VMs, which shows
forager's SSH credentials already work on them.

Root causes:
1. **The discovery datasource does not expose `ssh_command`.** It only runs
   signed content packs, so relay registers it as type `discovery` only.
2. **Dynamic-mode ssh-proxy is unusable from the platform.** None of the three
   callers send `params.host`, so it rejects every request:
   - llm-server `executeSSHViaProxyAgent` (`llm/llm-server/tools/common_relay.go:544`)
   - runbook-server `executeSSHViaProxyAgentRunbook` (`runbook-server/services/relay/service_utils.go:848`)
   - api-server `proxy_ssh_command` (`api-server/services/eventrule/playbooks/action_proxy_ssh_command.go`)

   Nubi's `host_name` override is silently dropped on the vm_agent path.
3. **Nubi can only address a VM by integration name or `host` regex.**
   `ServerExecuteTool.IdentifyConfig` (`llm/llm-server/tools/tool_server.go:327`)
   has no link to discovered `cloud_resourses`.

## Goal

Any VM that forager discovers inside an ssh-enabled scope can be addressed by
Nubi, runbooks and playbooks by instance id, IP, name or rDNS. Commands go
llm-server/runbook/api → relay → forager `ssh_command`, and there is **one
`ssh` integration per forager scope**, not one per VM.

## Non-goals

- Per-VM integrations in the Integrations UI.
- Per-VM credentials (one credential set per scope).
- Windows / WinRM.
- Bastion / jump hosts.
- Command allowlisting beyond Nubi's existing approval flow (tracked separately).

## Decisions

- **Sibling datasource, not a re-typed discovery datasource.** When enabled,
  forager starts a second, dynamic-mode `ssh-proxy` datasource beside the
  discovery one, with id `<discovery-id>:ssh`.
  - Discovery stays type `discovery`, because the vmpackage cron depends on it.
  - Routing by `datasource_id` keeps working unchanged.
- **Opt-in.** A new discovery config flag `ssh_access: true`, off by default.
  Discovery credentials were granted for signed packs only, and ad-hoc commands
  expand that grant.
- **The platform always passes the target host per request.** Forager enforces
  `allowed_hosts` (= discovery `allowed_cidrs`). Relay signs `params`, so the
  signature covers the host.

---

## Workstream A — forager

**A1. `ssh_access` sibling datasource**
- Add `ssh_access bool` to the discovery config: the local YAML
  `config.DiscoveryDatasource`, the discovery `Config`, and cloud push.
- When it is true and SSH creds are present, register an additional ssh-proxy
  in `cmd/app.go` and in the cloud-push path. Its config:
  - id `<discovery-id>:ssh`, name `<discovery-name>-ssh`, type `ssh`
  - `host` empty (dynamic mode)
  - `allowed_hosts` = `allowed_cidrs`
  - `port` = discovery `port`
  - same `username` / `private_key` / `passphrase` / `password`
  - `known_hosts` = discovery `known_hosts_file` (both are file paths).
- The sibling appears in `datasource_inventory`, health and metadata like any
  other datasource.

**A2. Cloud-pushed discovery**
- Add a `discovery-proxy` case to `newProxyByType` (`pkg/ws/handler.go:267`).
- Add `discovery-proxy` to the greeting `capabilities` (`pkg/ws/client.go:170`).

**A3. Fail closed for ad-hoc commands**
- When `ssh_access` is true, refuse to start the sibling in either case:
  - `signing_public_key` is not configured. Log an error and report an
    unhealthy datasource.
  - Host-key verification is not configured.
- Today an unsigned `ssh_command` is only warned about
  (`pkg/ws/handler.go:119`, `verifier.Enabled()`).

**Acceptance**
- [ ] Discovery config with `ssh_access: true`, creds, signing key and
      known_hosts: inventory contains `local:<name>` (`discovery-proxy`) and
      `local:<name>:ssh` (`ssh-proxy`).
- [ ] `ssh_command` with `params.host` inside `allowed_cidrs` returns
      `{stdout, stderr, exit_code}`.
- [ ] `ssh_command` with a host outside the CIDRs returns an error and does not dial.
- [ ] With `ssh_access: false` (default), inventory is unchanged from today.
- [ ] With `ssh_access: true` but no signing key or no known_hosts, the sibling
      is not registered and the reason is logged.
- [ ] A discovery datasource pushed via `datasource_config_sync` is instantiated
      (no "unknown proxy type" log).
- [ ] Unit tests cover the sibling construction and each of the three rejection paths.

## Workstream B — nudgebee: pass the target host through

**B1. llm-server**
- `executeSSHViaProxyAgent` accepts the `configs` map from `ExecuteContainerJob`.
- It sets `params.host` using this order:
  1. per-call `host_name`
  2. the integration's `host` config value
  3. the host resolved by C1
- If none resolves and the integration is dynamic mode, return a clear error
  ("no target host for dynamic ssh integration <name>"). Do not send the request.

**B2. runbook-server**
- `executeSSHViaProxyAgentRunbook` forwards the task's target host.
- Add a `host` input to the runbook SSH task if one is not already present.

**B3. api-server**
- `proxy_ssh_command` takes a `host` param and forwards it.
- Also add a vm_agent branch to the `ssh` playbook action (`integrations/ssh.go:385`),
  delegating to the same relay call.

**Acceptance**
- [ ] Each of the three callers sends `params.host` when one is given. Covered
      by a unit test per caller asserting the relay payload.
- [ ] Nubi `server_command_executor` with `instance=<fleet ssh integration>`
      and `host_name=<in-scope IP>` runs `uname -a` on that VM.
- [ ] A dynamic integration with no resolvable host fails before the relay call
      with the error above.
- [ ] Static-host integrations (one fixed `host`, no `mode: dynamic` label) behave
      exactly as before.

## Workstream C — nudgebee: resolve a VM to integration + host

**C1. VM resolution in `ServerExecuteTool.IdentifyConfig`**
- Try the existing name / host-regex match first.
- Otherwise, for each enabled `ssh` vm_agent integration without a static
  `host`, look up `cloud_resourses` in the integration's linked account(s).
  - Match `instance` case-insensitively against `resourse_id`, `name`,
    `meta.PrivateIpAddress` and rDNS.
  - Keep the VM only if its IP is inside the integration's
    `labels.allowed_hosts`. ssh-proxy metadata already reports this; relay
    writes it into `integrations.labels`.
  - Return a copy of the config with `host=<ip>` set.
- **Ambiguity:** if one VM matches several integrations (e.g. two accounts
  cover the same CIDRs), prefer an integration on the conversation's account.
  Otherwise return the candidates to the planner. Do not pick arbitrarily.

**C2. Planner visibility**
- Add ssh-reachable VMs to the execution-target list (`llm/llm-server/tools/execution_targets.go`,
  today kubectl + AWS only),
  so Nubi can propose "run on <instance id>" without the user naming an integration.
- Only VMs matching C1's rules count, capped and sorted by `last_seen`.

**C3. Cache**
- Relay sends an `integration_changed` replica broadcast (keys: account ids,
  channel `nb_replica_broadcast`; llm-server handles it in
  `llm/llm-server/api/integration_changes.go`) after
  `UpsertAgentDatasources`, stale removal, and metadata updates that change
  `labels`.

**Acceptance**
- [ ] "check disk usage on <instance id>" resolves to the scope's ssh sibling
      with host = the VM's IP and returns `df -h` output.
- [ ] The same works when the instance is given by IP.
- [ ] A VM outside `allowed_hosts` is not resolvable and gets a clear "no ssh
      access for <vm>" error.
- [ ] Two accounts whose scopes cover the same VMs yields a
      deterministic choice or an explicit disambiguation prompt, never a random pick.
- [ ] A newly registered ssh sibling is usable by Nubi within one relay
      round-trip (no TTL wait).

## Workstream D — nudgebee: multi-agent safety (required before GA)

Accounts can already run more than one proxy agent, so both issues below are
live bugs, not hypotheticals.

**D1. Stale cleanup scoped to the reporting agent**
- Today `removeStaleAgentDatasources` (`relay-server/pkg/db/db_store.go:629`)
  deletes every `source='agent'` integration on the account that is missing
  from *this* agent's inventory.
- Fix: write an `agent_id` config value on upsert and filter deletion by it.
  Rows without `agent_id` are left alone, not deleted (migration safety).

**D2. Request routing to the owning agent**
- Today all proxy agents on an account consume `relay_requests_<acct>_proxy`
  (`handlers/register.go:104`).
- Fix: route by the agent that owns the `datasource_key`, e.g. a per-agent queue
  `relay_requests_<acct>_proxy_<agent_id>`, with relay resolving
  datasource → agent on `/request`.

**Acceptance**
- [ ] Two foragers on one account with disjoint datasources: reconnecting either
      one does not delete the other's integrations.
- [ ] `ssh_command` for agent A's datasource is always handled by agent A
      (test with both connected, 50 requests, 0 misroutes).

## Workstream E — UI and audit (after A–C)

- VM inventory shows SSH access: which integration/agent provides it, and the
  last successful inventory as "last verified".
- Every `ssh_command` is logged with caller (Nubi conversation / runbook /
  playbook / user), VM resource id, host, command, exit code and duration.

---

## Rollout

1. Ship A. Enable `ssh_access` on one forager in a non-production
   environment, with a signing key and known_hosts.
2. Ship B1 and verify B's second acceptance item by hand.
3. Ship C. Verify end to end with Nubi.
4. Ship D before enabling on any account with more than one proxy agent.
5. Ship B2, B3 and E.

## Open questions

- When two accounts scan the same VMs, is overlapping scope a supported setup (which makes C1 disambiguation mandatory)?
- Should Nubi's commands on VMs go through the existing approval flow for
  mutating commands, or be read-only by default for discovered VMs?
