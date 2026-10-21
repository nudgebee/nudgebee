# VM-account scenarios (`type: vm_event`)

The same alert -> event -> `/v1/analyze/event/rca` pipeline as the Kubernetes
scenarios, aimed at a VM account (`account_type=vm`) whose hosts are managed
through a forager. Three things differ:

- **The alert goes to the account's generic event webhook**, not Alertmanager.
  Alertmanager builds Kubernetes subjects from `deployment`/`namespace`; the
  event webhook keeps the labels as sent, and the platform links the event to a
  VM from `instance_id` (or `instance`).
- **A fault is injected on a real host over SSH** (`vm/fault.sh`) and always
  restored, even when the run fails. VM accounts run systemd services only, so
  every fault is a transient systemd unit (`nb-bench-logger.service`,
  `nb-bench-spin.service`, `nb-bench-oom.service`): it appears in `systemctl status` and
  `journalctl -u`, and node-agent sees it as `/system.slice/<unit>.service`.
  Every unit has `RuntimeMaxSec`, so a failed restore cannot leave a host broken.
- **The queries are scored too.** After the RCA, the metrics and logs calls the
  investigation made are read back from `llm_conversation_tool_calls` and
  checked: queried at all, scoped to the alerted host, returned data, and never
  asked about another host. Reported as `row["telemetry"]`, separate from the
  diagnosis score, because a good-sounding diagnosis can rest on the wrong
  host's series or an empty result.

CLI only. The dashboard image has neither `ssh` nor a route to the fleet, so
`vm_event` is not registered in `llm/agents/event_code_fix` (like `pr_followup`).

## Scenarios

| # | Scenario | Alert | Fault | The point |
|---|---|---|---|---|
| 010 | `vm_disk_runaway_log` | host: `/var` 97% full | a service writes one error line into a log until it is MBs large | disk filling, found in metrics (and in logs once shipped) |
| 011 | `vm_cpu_spin` | host: CPU above 90% | a service pins one core | a host alert, culprit found over SSH and metrics |
| 012 | `vm_service_cpu_spin` | service: CPU of one unit, `container_id=/system.slice/nb-bench-spin.service` | the same fault as 011 | the same fault raised per service: the unit's own series must be read |
| 013 | `vm_service_oom` | service: `ServiceOOMKilled` | a service with `MemoryMax=64M` outgrows it and is OOM-killed | the unit's limit, not host memory pressure, is the cause |
| 014 | `vm_service_memory_leak_code_fix` | service: `ServiceOOMKilled`, `container_id=/system.slice/memory-leak-oom.service` | the `nb-benchmark-fixtures` `memory_leak_oom` worker, installed as a real unit, leaks until OOM-killed | the unit is linked to its repository, so the investigation can reach the code-fix stage; the returned diff is run against the fixture's tests |

012 and 013 carry `container_id`, which for a systemd service is its cgroup
path, and add `expected_telemetry.service_markers`: a metrics query must name
the unit as well as the host, or `metrics_scoped_to_service` fails. Whether an
`event_webhook` alert's `container_id` is picked up for a linked repository
(`vmServiceCodeLink`) is not verified; it does not change these scores, since
no repository is linked.

## Prerequisites

| Need | Why |
|---|---|
| A VM account with a forager, and two or more hosts it has discovered | the fleet; host B is the wrong-host control |
| An `event_webhook` integration linked to that account | where the alert is posted |
| The hosts' metrics and logs reachable through the account's default providers | what the investigation queries |
| promtail/alloy shipping `/var/log/nb-bench/*.log` on host A (optional) | the `disk_runaway_log` fault is only visible to logs if shipped; scenario 010 expects metrics only until it is, and you add `logs` to its `signals` |
| Non-interactive `ssh` from the machine running the benchmark to each host, with passwordless `sudo` and systemd 236+ (`systemd-run`) | fault injection |
| cgroup v2 on the faulty host (`MemoryMax`, `MemorySwapMax`) | scenario 013; on cgroup v1 the limit is ignored and the unit is never killed |
| The forager's SSH user can read the journal (e.g. `systemd-journal` group) | without it the investigation can read `systemctl status` but not `journalctl -u`, so service-level conclusions stay inconclusive (seen in `docs/vm-orchestrator-proposal.md`) |
| `APP_DATABASE_URL` | reading back the issued queries; without it telemetry is `not_measured`, not failed |

## Fleet variables

Scenarios use `${NAME}` placeholders; nothing about a fleet is committed. A
scenario with an unset placeholder stops with `bucket=setup_error` before any
fault is injected.

| Variable | Meaning |
|---|---|
| `EVENT_WEBHOOK_URL` | the integration's full webhook URL including `?token=` |
| `EVENT_WEBHOOK_INTEGRATION_ID` | that integration's id (the platform stores fingerprints as `ew:<id>:fp:<fingerprint>`) |
| `VM_A_INSTANCE_ID` | cloud instance id of the faulty host (`cloud_resourses.resourse_id`), sent as the alert's `instance_id` |
| `VM_A_HOST` | its hostname, as it appears in logs |
| `VM_A_INSTANCE` | the `instance` label value the metrics store holds for it (node-agent hashes it; plain node_exporter uses `ip:port`) |
| `VM_A_SSH` | what `ssh` accepts to reach it (`user@host` or an `~/.ssh/config` Host) |
| `VM_A_FILL_MB` | size of the runaway log, MB (1-4096); pick well under the free space of `/var` |
| `FIXTURE_DIR` | scenario 014 only: a local checkout of `nudgebee/nb-benchmark-fixtures` at the scenario's `bug_commit` (the repo is private, so the host cannot clone it; the fault streams `memory_leak_oom/` over `ssh`) |
| `VM_B_HOST`, `VM_B_INSTANCE` | the healthy control host; querying it is a wrong-host read |

## Running

```bash
export EVENT_WEBHOOK_URL=... EVENT_WEBHOOK_INTEGRATION_ID=...
export VM_A_INSTANCE_ID=... VM_A_HOST=... VM_A_INSTANCE=... VM_A_SSH=... VM_A_FILL_MB=...
export VM_B_HOST=... VM_B_INSTANCE=...
python run_event_code_fix.py --scenario vm_cpu_spin --scenario vm_disk_runaway_log
```

Reading a result: `bucket` is the diagnosis-only bucket (these scenarios have no
`fixture_repo`, so `diagnosis_only`); `telemetry.checks` names exactly which
check failed. `<signal>_queried` failing means the investigation never used
that backend; `_scoped_to_target` means it queried without the host's marker;
`_scoped_to_service` means no host-scoped query named the unit;
`_nonempty` means the scoped query returned nothing.

## Status

The scenarios and the harness are unit-tested (`test_vm_event.py`), but the
fault commands and the live path have not been run against a fleet yet. The
first run against the current platform is the baseline: before the VM
orchestrator is given metrics and logs tools, `*_queried` is expected to fail.
Run each scenario several times; the telemetry checks are deterministic but the
diagnosis score is not. Each run posts its alert under a fingerprint with a fresh suffix, so a
rerun investigates again instead of reusing the first run's finished investigation.

Scenario 014 is the only code-fix (`fixture_repo`) VM scenario. After the event
exists, the run links the unit to its repository through the same
`cloud_resource_attributes_upsert` action the UI's "Link repository" uses, and
clears the link afterwards. It needs `APP_DATABASE_URL` (to find the VM the event
was linked to), `ACTION_API_SERVER_TOKEN` and a platform where a linked VM
service reaches the code-fix stage; on one where it does not, the row's bucket
says the stage was skipped. The install, link and fix path have not been run
against a fleet yet. The host needs `python3` and cgroup v2.
