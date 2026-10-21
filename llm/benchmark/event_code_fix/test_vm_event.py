import subprocess
from pathlib import Path

import pytest

import run_event_code_fix as runner
import telemetry_scorer
import vm_scenario
from scenario_loader import load_scenarios

SCENARIOS_DIR = Path(__file__).parent / "scenarios"
FLEET = {
    "VM_A_INSTANCE_ID": "i-aaa",
    "VM_A_HOST": "vm-a.internal",
    "VM_A_INSTANCE": "a1b2c3",
    "VM_A_SSH": "ops@vm-a.internal",
    "VM_A_FILL_MB": "512",
    "VM_B_HOST": "vm-b.internal",
    "VM_B_INSTANCE": "d4e5f6",
    "FIXTURE_DIR": "/fx",
}


def call(tool, parameters, status="success", response='[{"v": 1}]'):
    return {
        "tool": tool,
        "parameters": parameters,
        "status": status,
        "response": response,
    }


# --- placeholder expansion ---------------------------------------------------


def test_expand_env_replaces_set_names_and_keeps_unset_ones(monkeypatch):
    monkeypatch.setenv("VM_A_HOST", "vm-a.internal")
    monkeypatch.delenv("VM_NOT_SET", raising=False)
    out = vm_scenario.expand_env(
        {"a": ["on ${VM_A_HOST}", 3], "b": {"c": "${VM_NOT_SET}"}}
    )
    assert out == {"a": ["on vm-a.internal", 3], "b": {"c": "${VM_NOT_SET}"}}
    assert vm_scenario.unresolved_placeholders(out) == {"VM_NOT_SET"}


def test_unresolved_placeholders_of_a_fully_resolved_value_is_empty():
    assert (
        vm_scenario.unresolved_placeholders({"a": ["x", {"b": "y"}], "c": []}) == set()
    )


def test_loader_expands_only_vm_event_scenarios(monkeypatch):
    for name, value in FLEET.items():
        monkeypatch.setenv(name, value)
    scenarios = {s["name"]: s for s in load_scenarios(SCENARIOS_DIR)}

    disk = scenarios["vm_disk_runaway_log"]
    assert disk["alert"]["labels"]["instance_id"] == "i-aaa"
    assert disk["vm"]["inject"] == [
        "vm/fault.sh disk_runaway_log inject ops@vm-a.internal 512"
    ]
    assert vm_scenario.unresolved_placeholders(disk) == set()
    # A Kubernetes scenario is left exactly as written.
    assert scenarios["memory_leak_oom"]["alert"]["labels"]["namespace"] == (
        "benchmark-scenarios"
    )


VM_SCENARIOS = [
    "vm_disk_runaway_log",
    "vm_cpu_spin",
    "vm_service_cpu_spin",
    "vm_service_oom",
    "vm_service_memory_leak_code_fix",
]


def loaded_vm_scenarios(monkeypatch):
    for key, value in FLEET.items():
        monkeypatch.setenv(key, value)
    return {
        s["name"]: s
        for s in load_scenarios(SCENARIOS_DIR)
        if s.get("type") == "vm_event"
    }


def test_every_vm_scenario_is_covered_and_has_its_own_identity(monkeypatch):
    scenarios = loaded_vm_scenarios(monkeypatch)

    assert sorted(scenarios) == sorted(VM_SCENARIOS)
    fingerprints = [s["alert"]["fingerprint"] for s in scenarios.values()]
    assert len(set(fingerprints)) == len(fingerprints)


@pytest.mark.parametrize("name", ["vm_service_cpu_spin", "vm_service_oom"])
def test_service_alerts_carry_the_units_cgroup_path(name, monkeypatch):
    scenario = loaded_vm_scenarios(monkeypatch)[name]

    container_id = scenario["alert"]["labels"]["container_id"]
    assert container_id.startswith("/system.slice/")
    assert container_id.endswith(".service")
    markers = scenario["expected_telemetry"]["service_markers"]
    assert markers and all(m in container_id for m in markers)


@pytest.mark.parametrize("name", VM_SCENARIOS)
def test_vm_scenarios_declare_what_the_runner_needs(name, monkeypatch):
    for key, value in FLEET.items():
        monkeypatch.setenv(key, value)
    scenario = {s["name"]: s for s in load_scenarios(SCENARIOS_DIR)}[name]

    assert scenario["type"] == "vm_event"
    assert scenario["alert"]["fingerprint"]
    assert scenario["vm"]["inject"] and scenario["vm"]["restore"]
    assert scenario["expected_diagnosis"]
    assert scenario["expected_telemetry"]["target_markers"]
    assert set(scenario["expected_telemetry"]["signals"]) <= set(
        telemetry_scorer.SIGNAL_TOOLS
    )


# --- telemetry scoring -------------------------------------------------------

EXPECTED = {
    "signals": ["metrics", "logs"],
    "target_markers": ["a1b2c3", "vm-a.internal"],
    "forbidden_markers": ["d4e5f6"],
}


def failed(result):
    return {k for k, v in result["checks"].items() if not v["passed"]}


def test_scoped_queries_with_data_pass():
    result = telemetry_scorer.score_telemetry(
        EXPECTED,
        [
            call(
                "prometheus_execute", 'node_filesystem_avail_bytes{instance="a1b2c3"}'
            ),
            call("logs_execute_v2", '{"where": {"host": "vm-a.internal"}}'),
        ],
    )
    assert result["passed"] is True
    assert failed(result) == set()


def test_no_calls_fails_every_signal_as_never_queried():
    result = telemetry_scorer.score_telemetry(EXPECTED, [])
    assert {"metrics_queried", "logs_queried"} <= failed(result)
    assert result["passed"] is False


def test_unscoped_query_is_queried_but_not_scoped():
    result = telemetry_scorer.score_telemetry(
        {**EXPECTED, "signals": ["metrics"]},
        [call("prometheus_execute", "avg(node_cpu_seconds_total)")],
    )
    assert failed(result) == {"metrics_scoped_to_target", "metrics_nonempty"}


def test_scoped_query_returning_nothing_fails_only_nonempty():
    empty = call(
        "logs_execute_v2", '{"where": {"host": "vm-a.internal"}}', response="[]"
    )
    result = telemetry_scorer.score_telemetry(
        {**EXPECTED, "signals": ["logs"]}, [empty]
    )
    assert failed(result) == {"logs_nonempty"}


def test_a_failed_call_does_not_count_as_data():
    errored = call(
        "prometheus_execute", 'up{instance="a1b2c3"}', status="error", response="boom"
    )
    result = telemetry_scorer.score_telemetry(
        {**EXPECTED, "signals": ["metrics"]}, [errored]
    )
    assert failed(result) == {"metrics_nonempty"}


def test_querying_a_forbidden_host_fails_even_when_the_target_was_read():
    result = telemetry_scorer.score_telemetry(
        {**EXPECTED, "signals": ["metrics"]},
        [
            call("prometheus_execute", 'up{instance="a1b2c3"}'),
            call("prometheus_execute", 'up{instance="d4e5f6"}'),
        ],
    )
    assert failed(result) == {"no_other_host_queried"}


def test_missing_target_markers_is_a_scenario_error():
    with pytest.raises(ValueError):
        telemetry_scorer.score_telemetry({"signals": ["metrics"]}, [])


SERVICE_EXPECTED = {
    "signals": ["metrics"],
    "target_markers": ["a1b2c3"],
    "service_markers": ["nb-bench-spin"],
}


def test_service_alert_needs_a_host_scoped_query_that_names_the_unit():
    host_only = call("prometheus_execute", 'node_cpu_seconds_total{instance="a1b2c3"}')
    unit_and_host = call(
        "prometheus_execute",
        'container_cpu{instance="a1b2c3",container_id="/system.slice/nb-bench-spin.service"}',
    )

    only_host = telemetry_scorer.score_telemetry(SERVICE_EXPECTED, [host_only])
    both = telemetry_scorer.score_telemetry(
        SERVICE_EXPECTED, [host_only, unit_and_host]
    )

    assert failed(only_host) == {"metrics_scoped_to_service"}
    assert failed(both) == set()


def test_a_unit_query_on_no_particular_host_does_not_satisfy_the_service_check():
    unit_only = call(
        "prometheus_execute",
        'container_cpu{container_id="/system.slice/nb-bench-spin.service"}',
    )

    result = telemetry_scorer.score_telemetry(SERVICE_EXPECTED, [unit_only])

    assert {"metrics_scoped_to_target", "metrics_scoped_to_service"} <= failed(result)


def test_service_markers_are_optional():
    result = telemetry_scorer.score_telemetry(
        {"signals": ["metrics"], "target_markers": ["a1b2c3"]},
        [call("prometheus_execute", 'up{instance="a1b2c3"}')],
    )

    assert "metrics_scoped_to_service" not in result["checks"]
    assert result["passed"] is True


# --- runner: inject, restore, score ------------------------------------------


class FakeClient:
    account_id = "acct"
    tenant_id = "tenant"
    user_id = "user"

    def unlink_service_repos(self):
        pass


def vm_scenario_dict(**vm):
    return {
        "name": "s",
        "type": "vm_event",
        "alert": {"fingerprint": "fp"},
        "vm": {"inject": ["inject-1"], "restore": ["restore-1"], **vm},
        "expected_telemetry": {"signals": ["metrics"], "target_markers": ["a1b2c3"]},
    }


@pytest.fixture
def steps(monkeypatch):
    log = []
    monkeypatch.setattr(
        runner, "run_commands", lambda cmds: log.append(("inject", cmds))
    )
    monkeypatch.setattr(
        runner, "restore_commands", lambda cmds: log.append(("restore", cmds))
    )
    monkeypatch.setattr(
        runner,
        "_run_event_pipeline",
        lambda *a: log.append(("pipeline",))
        or ({"name": "s", "bucket": "diagnosis_only"}, {"event_fingerprint": "ew:x"}),
    )
    monkeypatch.setattr(
        runner, "telemetry_calls", lambda rca, account: log.append(("telemetry",)) or []
    )
    return log


def test_fault_is_injected_before_and_restored_after_the_pipeline(steps):
    row, rca = runner.run_event_pipeline(FakeClient(), vm_scenario_dict(), False)

    assert [s[0] for s in steps] == ["inject", "pipeline", "restore", "telemetry"]
    assert row["bucket"] == "diagnosis_only"
    assert row["telemetry"]["passed"] is False  # no calls were recorded
    assert rca is not None


def test_unset_fleet_variables_stop_the_run_before_any_fault(steps):
    scenario = vm_scenario_dict()
    scenario["alert"]["labels"] = {"instance_id": "${VM_A_INSTANCE_ID}"}

    row, rca = runner.run_event_pipeline(FakeClient(), scenario, False)

    assert row["bucket"] == "setup_error"
    assert "VM_A_INSTANCE_ID" in row["detail"]
    assert rca is None
    assert steps == []


def test_failed_injection_is_a_setup_error_and_still_restores(steps, monkeypatch):
    def broken(cmds):
        raise OSError("ssh: connection refused")

    monkeypatch.setattr(runner, "run_commands", broken)

    row, rca = runner.run_event_pipeline(FakeClient(), vm_scenario_dict(), False)

    assert row["bucket"] == "setup_error"
    assert "connection refused" in row["detail"]
    assert rca is None
    assert [s[0] for s in steps] == ["restore"]


def test_restore_failure_is_reported_even_when_injection_failed(steps, monkeypatch):
    def broken(cmds):
        raise OSError("ssh: connection refused")

    monkeypatch.setattr(runner, "run_commands", broken)
    monkeypatch.setattr(runner, "restore_commands", lambda cmds: "host unreachable")

    row, _ = runner.run_event_pipeline(FakeClient(), vm_scenario_dict(), False)

    assert row["bucket"] == "setup_error"
    assert row["restore_failed"] == "host unreachable"


def test_failed_command_error_carries_its_stderr():
    with pytest.raises(subprocess.SubprocessError, match="Permission denied"):
        vm_scenario.run_commands(["echo 'Permission denied (publickey)' >&2; exit 255"])


def test_restore_failure_is_reported_on_the_row(steps, monkeypatch):
    monkeypatch.setattr(runner, "restore_commands", lambda cmds: "host unreachable")

    row, _ = runner.run_event_pipeline(FakeClient(), vm_scenario_dict(), False)

    assert row["restore_failed"] == "host unreachable"


def test_unmeasured_telemetry_is_not_reported_as_a_failure(steps, monkeypatch):
    monkeypatch.setattr(runner, "telemetry_calls", lambda rca, account: None)

    row, _ = runner.run_event_pipeline(FakeClient(), vm_scenario_dict(), False)

    assert row["telemetry"]["status"] == "not_measured"
    assert "passed" not in row["telemetry"]


def test_kubernetes_scenarios_skip_the_vm_wrapper(steps):
    row, _ = runner.run_event_pipeline(
        FakeClient(), {"name": "k", "type": "event", "alert": {}}, False
    )

    assert [s[0] for s in steps] == ["pipeline"]
    assert "telemetry" not in row


# --- linking a VM service to its repository ----------------------------------


def test_link_service_repo_upserts_repo_and_branch_rows_for_the_events_vm(monkeypatch):
    import event_rca_client

    sent = []

    class Resp:
        def raise_for_status(self):
            pass

    monkeypatch.setattr(event_rca_client, "event_resource_id", lambda e: "res-1")
    monkeypatch.setattr(
        event_rca_client.requests,
        "post",
        lambda url, **kw: sent.append((url, kw)) or Resp(),
    )
    client = event_rca_client.EventCodeFixClient.__new__(
        event_rca_client.EventCodeFixClient
    )
    client.api_url, client.api_token = "http://api", "tok"
    client.account_id, client.tenant_id = "acc", "ten"
    client._linked = []

    client.link_service_repo(
        "evt", "/system.slice/x.service", "https://github.com/o/r", "rel"
    )
    client.unlink_service_repos()

    link, unlink = sent
    assert link[0] == "http://api/rpc/tenant"
    assert link[1]["json"]["action"] == {"name": "cloud_resource_attributes_upsert"}
    rows = {o["name"]: o["value"] for o in link[1]["json"]["input"]["objects"]}
    assert rows == {
        "workloads.nudgebee.com/git.repo//system.slice/x.service": "https://github.com/o/r",
        "workloads.nudgebee.com/git.branch//system.slice/x.service": "rel",
    }
    assert {o["resource_id"] for o in link[1]["json"]["input"]["objects"]} == {"res-1"}
    # An empty value is how the UI unlinks.
    assert {o["value"] for o in unlink[1]["json"]["input"]["objects"]} == {""}
    assert client._linked == []


def test_link_service_repo_without_a_vm_resource_fails_loudly(monkeypatch):
    import event_rca_client

    monkeypatch.setattr(event_rca_client, "event_resource_id", lambda e: None)
    client = event_rca_client.EventCodeFixClient.__new__(
        event_rca_client.EventCodeFixClient
    )
    client._linked = []
    with pytest.raises(RuntimeError, match="no VM resource"):
        client.link_service_repo(
            "evt", "/system.slice/x.service", "https://github.com/o/r"
        )


def test_code_fix_scenario_loads_with_a_pinned_fixture_and_a_link(monkeypatch):
    for name, value in {**FLEET, "FIXTURE_DIR": "/fx"}.items():
        monkeypatch.setenv(name, value)
    scenario = {s["name"]: s for s in load_scenarios(SCENARIOS_DIR)}[
        "vm_service_memory_leak_code_fix"
    ]
    assert vm_scenario.unresolved_placeholders(scenario) == set()
    assert len(scenario["fixture_repo"]["bug_commit"]) == 40
    assert (
        scenario["vm"]["link_repo"]["container_id"]
        == scenario["alert"]["labels"]["container_id"]
    )


def test_each_vm_event_post_gets_its_own_fingerprint(monkeypatch):
    import event_rca_client

    posted = []

    class Resp:
        def raise_for_status(self):
            pass

    monkeypatch.setattr(
        event_rca_client.requests,
        "post",
        lambda url, **kw: posted.append(kw["json"]["events"][0]) or Resp(),
    )
    client = event_rca_client.EventCodeFixClient.__new__(
        event_rca_client.EventCodeFixClient
    )
    client.event_webhook_url = "http://hook"
    client.event_webhook_integration_id = "int-1"
    scenario = {
        "alert": {"fingerprint": "bench-x-1", "alertname": "A", "log_lines": "l"}
    }

    first = client.post_vm_event(scenario)
    second = client.post_vm_event(scenario)

    assert first != second
    for key, event in zip((first, second), posted):
        assert key == f"ew:int-1:fp:{event['fingerprint']}"
        assert event["id"] == event["fingerprint"]
        assert event["fingerprint"].startswith("bench-x-1-")


@pytest.mark.parametrize(
    "bucket,finished",
    [
        ("diagnosis_only", True),
        ("passed", True),
        ("skip:no_repository_mapped", True),
        ("error", False),
        ("error:rca_failed", False),
        ("setup_error", False),
        ("ingestion_timeout", False),
    ],
)
def test_only_rows_that_reached_a_verdict_count_as_already_run(bucket, finished):
    assert runner._finished({"name": "s", "bucket": bucket}) is finished


def test_disk_scenario_does_not_expect_logs_nothing_ships(monkeypatch):
    scenario = loaded_vm_scenarios(monkeypatch)["vm_disk_runaway_log"]
    assert scenario["expected_telemetry"]["signals"] == ["metrics"]


def test_event_lookup_survives_a_dropped_connection(monkeypatch):
    import event_rca_client

    class Resp:
        def raise_for_status(self):
            pass

        def json(self):
            return {"rows": [["evt-1"]]}

    calls = []

    def flaky(url, **kw):
        calls.append(url)
        if len(calls) < 3:
            raise event_rca_client.requests.ConnectionError("connection refused")
        return Resp()

    monkeypatch.setattr(event_rca_client.requests, "post", flaky)
    monkeypatch.setattr(event_rca_client.time, "sleep", lambda s: None)
    client = event_rca_client.EventCodeFixClient.__new__(
        event_rca_client.EventCodeFixClient
    )
    client.api_url, client.api_token = "http://api", "tok"
    client.account_id, client.tenant_id = "acc", "ten"

    assert client.wait_for_event("fp") == "evt-1"
    assert len(calls) == 3


def test_rca_poll_survives_a_dropped_connection(monkeypatch):
    import event_rca_client

    class Resp:
        def raise_for_status(self):
            pass

        def json(self):
            return {"data": {"status": "completed", "event_id": "evt-1"}}

    calls = []

    def flaky(url, **kw):
        calls.append(url)
        if len(calls) == 1:
            raise event_rca_client.requests.ConnectionError("remote end closed")
        return Resp()

    monkeypatch.setattr(event_rca_client.requests, "post", flaky)
    monkeypatch.setattr(event_rca_client.time, "sleep", lambda s: None)
    client = event_rca_client.EventCodeFixClient.__new__(
        event_rca_client.EventCodeFixClient
    )
    client.llm_url, client.llm_token = "http://llm", "tok"
    client.account_id, client.tenant_id = "acc", "ten"

    assert client.run_rca("evt-1")["status"] == "completed"
    assert len(calls) == 2


def test_event_wait_budget_covers_a_slow_ingestion_and_can_be_overridden(monkeypatch):
    import event_rca_client

    monkeypatch.delenv("EVENT_WAIT_SECONDS", raising=False)
    assert event_rca_client.event_wait_seconds() >= 240
    monkeypatch.setenv("EVENT_WAIT_SECONDS", "45")
    assert event_rca_client.event_wait_seconds() == 45
