"""Discovery cleanup must close ONLY the events of resources it just deactivated.

Regression cover for the sweep that closed an account's entire open event list on
every snapshot: it compared `events.service_key` against `active_resources.resourse_id`,
two identifiers that never share a format, so `service_key != ALL(active)` matched every
row -- live resources included. Observed live: a pod in CrashLoopBackOff had all four of
its events (agent, datadog, pagerduty) closed by a *Job* discovery pass while the pod was
still crashing.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_resolved_event_notify.py.
"""

import os
import sys
import unittest
from unittest import mock

_ENV_DEFAULTS = {
    "ENV": "DEV",
    "COLLECTOR_MODE": "worker",
    "COLLECTOR_DB_URL": "postgresql://u:p@localhost:5432/db?sslmode=disable",
    "NUDGEBEE_ENCRYPTION_KEY": "test-key",
    "ACTION_API_SERVER_TOKEN": "",
    "SERVICE_API_SERVER_URL": "http://localhost:8000",
    "RABBIT_MQ_HOST": "localhost",
    "RABBIT_MQ_PORT": "5672",
    "RABBIT_MQ_USERNAME": "guest",
    "RABBIT_MQ_PASSWORD": "guest",
    "REDIS_SERVER_HOST": "localhost",
    "REDIS_SERVER_PORT": "6379",
    "REDIS_USER_NAME": "",
    "REDIS_USER_PASSWORD": "",
    "CLICKHOUSE_ENABLED": "false",
    "CLICKHOUSE_HOST": "",
    "CLICKHOUSE_USER": "",
    "CLICKHOUSE_PASSWORD": "",
}
for _k, _v in _ENV_DEFAULTS.items():
    os.environ.setdefault(_k, _v)

for _mod in (
    "redis",
    "psycopg2",
    "psycopg2.extras",
    "psycopg2.pool",
    "clickhouse_driver",
    "clickhouse_connect",
    "clickhouse_connect.driver",
    "clickhouse_connect.driver.client",
    "clickhouse_connect.driver.query",
    "clickhouse_connect.driver.summary",
):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

import handlers.discovery_handler as dh  # noqa: E402
from datetime import datetime, timedelta, timezone  # noqa: E402

ACCOUNT = "acct-1"
TENANT = "tenant-1"
# The Deployment that is alive and whose pod is crashlooping.
LIVE_RESOURCE = "a61d9787-d0d4-56dc-95fa-05989036fadd"
GONE_RESOURCE = "b0000000-0000-0000-0000-00000000dead"


class _Recorder:
    """Stands in for database.run_query, remembering every statement executed."""

    def __init__(self, deactivated):
        self.deactivated = deactivated
        self.queries = []

    def __call__(self, query, values=None, cursor_factory=None):
        text = query if isinstance(query, str) else str(query)
        self.queries.append((" ".join(text.split()), values))
        if "UPDATE cloud_resourses" in text and "RETURNING id" in text:
            return [(r,) for r in self.deactivated]
        return []

    def statements(self, needle):
        return [q for q, _ in self.queries if needle in q]


class TestDiscoveryCleanupScoping(unittest.TestCase):
    def _run(self, resource_type, deactivated, active_resources=None):
        rec = _Recorder(deactivated)
        active = active_resources or [(LIVE_RESOURCE, f"ns/{resource_type}/live")]
        with mock.patch.object(dh.database, "run_query", side_effect=rec) as run_query, mock.patch.object(
            dh, "close_events_with_history"
        ) as closer:
            # First call is the active_resources SELECT; the recorder returns [] for it,
            # so feed the active set through a side_effect wrapper instead.
            def _dispatch(query, values=None, cursor_factory=None):
                text = query if isinstance(query, str) else str(query)
                if "FROM active_resources" in text:
                    rec.queries.append((" ".join(text.split()), values))
                    return active
                return rec(query, values, cursor_factory)

            run_query.side_effect = _dispatch
            dh.handle_active_resources_deletion(ACCOUNT, TENANT, resource_type, total_resources_count=1)
        return rec, closer

    def test_no_event_is_closed_by_service_key(self):
        """The service_key predicate is gone -- it could never match across formats."""
        rec, _ = self._run("Job", deactivated=[])
        for stmt in rec.statements("UPDATE events"):
            self.assertNotIn("service_key", stmt)

    def test_live_resource_keeps_its_events(self):
        """A pass that deactivates nothing must not touch the events table at all."""
        rec, closer = self._run("Job", deactivated=[])
        closer.assert_not_called()
        self.assertEqual(rec.statements("UPDATE events"), [])

    def test_close_is_keyed_on_the_deactivated_resource_ids(self):
        _, closer = self._run("Pod", deactivated=[GONE_RESOURCE])
        closer.assert_called_once()
        kwargs = closer.call_args.kwargs
        self.assertIn("cloud_resource_id", kwargs["where_conditions"])
        self.assertNotIn("service_key", kwargs["where_conditions"])
        self.assertEqual(kwargs["params"], [[GONE_RESOURCE]])
        self.assertEqual(kwargs["closing_reason"], "resource_inactive")
        self.assertEqual(kwargs["metadata"]["resource_type"], "Pod")


class TestJobKindScoping(unittest.TestCase):
    """Jobs and CronJobs arrive in the job snapshot, never in the service snapshot.

    The workload reconcile diffs against the service snapshot, so it must leave
    both kinds alone; the Job reconcile is the one that owns them, CronJobs included.
    """

    def _statements(self, resource_type, total, active):
        queries = []

        def _dispatch(query, values=None, cursor_factory=None):
            text = " ".join(str(query).split())
            queries.append(text)
            return active if "FROM active_resources" in text else []

        with mock.patch.object(dh.database, "run_query", side_effect=_dispatch), mock.patch.object(
            dh, "close_events_with_history"
        ):
            dh.handle_active_resources_deletion(ACCOUNT, TENANT, resource_type, total_resources_count=total)
        return [q for q in queries if q.startswith("UPDATE")]

    def _both_paths(self, resource_type):
        """Statements from the active-set diff and from the zero-count bulk pass."""
        return [
            self._statements(resource_type, 1, [(LIVE_RESOURCE, "ns/Deployment/live")]),
            self._statements(resource_type, 0, []),
        ]

    def test_workload_reconcile_skips_jobs_and_cronjobs(self):
        for updates in self._both_paths("workload"):
            cloud = [q for q in updates if q.startswith("UPDATE cloud_resourses")]
            workloads = [q for q in updates if q.startswith("UPDATE k8s_workloads")]
            self.assertEqual(len(cloud), 1)
            self.assertEqual(len(workloads), 1)
            self.assertIn("'Job', 'CronJob')", cloud[0])
            self.assertIn("AND kind NOT IN ('Job', 'CronJob')", workloads[0])

    def test_job_reconcile_covers_cronjobs(self):
        for updates in self._both_paths("Job"):
            cloud = [q for q in updates if q.startswith("UPDATE cloud_resourses")]
            workloads = [q for q in updates if q.startswith("UPDATE k8s_workloads")]
            self.assertEqual(len(cloud), 1)
            self.assertEqual(len(workloads), 1)
            self.assertIn("type IN ('Job', 'CronJob')", cloud[0])
            self.assertIn("kind IN ('Job', 'CronJob')", workloads[0])

    def test_job_reconcile_reports_deactivated_workloads_to_the_graph(self):
        """CronJob deletions now land in the Job pass, so it must hand them to the graph."""
        returned = [(GONE_RESOURCE, "CronJob")]
        for total, active in ((1, [(LIVE_RESOURCE, "ns/Job/live")]), (0, [])):

            def _dispatch(query, values=None, cursor_factory=None):
                text = " ".join(str(query).split())
                if "FROM active_resources" in text:
                    return active
                return returned if text.startswith("UPDATE k8s_workloads") else []

            with mock.patch.object(dh.database, "run_query", side_effect=_dispatch), mock.patch.object(
                dh, "close_events_with_history"
            ), mock.patch.object(dh, "record_deleted_workloads") as recorder:
                dh.handle_active_resources_deletion(ACCOUNT, TENANT, "Job", total_resources_count=total)
            recorder.assert_called_once_with(returned)


# utc_from_epoch_millis() returns NAIVE UTC and WorkloadDetails.last_seen carries
# its .isoformat(), so naive is the shape production actually delivers.
OBSERVED_AT = datetime(2026, 8, 19, 8, 30)


def _go_container(started_ago, ready=True, last_reason=None, name="app"):
    """A Go agent containerStatuses entry (Kubernetes PodStatus, RFC 3339 `Z` times)."""
    status = {
        "name": name,
        "ready": ready,
        "restartCount": 0,
        "state": {"running": {"startedAt": (OBSERVED_AT - started_ago).strftime("%Y-%m-%dT%H:%M:%SZ")}},
    }
    if last_reason:
        finished = OBSERVED_AT - started_ago - timedelta(seconds=10)
        status["restartCount"] = 6
        status["lastState"] = {
            "terminated": {"reason": last_reason, "exitCode": 1, "finishedAt": finished.strftime("%Y-%m-%dT%H:%M:%SZ")}
        }
    return status


def _go_status(*containers):
    return {"phase": "Running", "containerStatuses": list(containers)}


def _python_status(started_ago, ready=True, last_reason=None):
    """The Python agent's shape: snake_case keys, `+00:00` times, explicit nulls."""
    terminated = None
    if last_reason:
        finished = OBSERVED_AT - started_ago - timedelta(seconds=10)
        terminated = {
            "reason": last_reason,
            "exit_code": 1,
            "finished_at": finished.strftime("%Y-%m-%d %H:%M:%S+00:00"),
        }
    return {
        "phase": "Running",
        "container_statuses": [
            {
                "name": "app",
                "ready": ready,
                "state": {
                    "running": {"started_at": (OBSERVED_AT - started_ago).strftime("%Y-%m-%d %H:%M:%S+00:00")},
                    "waiting": None,
                    "terminated": None,
                },
                "last_state": {"running": None, "waiting": None, "terminated": terminated},
            }
        ],
    }


# A pod one second into a crash cycle: Ready, running, restarted seconds ago -- what
# the agent's Deployment 1/1 actually coincides with.
CRASHLOOPING = _go_status(_go_container(timedelta(seconds=2), last_reason="Error"))
STABLE = _go_status(_go_container(timedelta(hours=3)))


class TestWorkloadRecoveryClose(unittest.TestCase):
    """The recovery close is DEFAULT ON (see Configs.EVENT_CLOSE_ON_WORKLOAD_RECOVERY).

    Readiness on its own is a single sample and closed live crashloops. These tests
    pin the checks that bound that: the workload counts, the per-pod readiness (a
    stuck rollout), and how long the containers have been running (a live crashloop
    sampled in the seconds its container is up, which the first two cannot see).
    """

    def setUp(self):
        enabled = mock.patch.object(dh.Configs, "EVENT_CLOSE_ON_WORKLOAD_RECOVERY", True)
        enabled.start()
        self.addCleanup(enabled.stop)
        self.unready_rows = []
        # None = every queried workload has one long-running pod.
        self.pod_rows = None

        def _query(query, params=None, *_, **__):
            if "status_info" in query:
                if self.pod_rows is not None:
                    return self.pod_rows
                return [(ns, name, STABLE) for ns in params[2] for name in params[3]]
            return self.unready_rows

        pods = mock.patch.object(dh.database, "run_query", side_effect=_query)
        self.pod_query = pods.start()
        self.addCleanup(pods.stop)

    OBSERVED_AT = OBSERVED_AT

    def _workload(self, resource_id, total, ready, last_seen="default", name="web"):
        w = mock.Mock()
        w.cloud_resource_id = resource_id
        w.tenant_id = TENANT
        w.namespace = "default"
        w.name = name
        w.total_pods = total
        w.ready_pods = ready
        w.last_seen = self.OBSERVED_AT.isoformat() if last_seen == "default" else last_seen
        return w

    def _queries(self, needle):
        return [c.args for c in self.pod_query.call_args_list if needle in c.args[0]]

    def test_all_pods_ready_closes_agent_events(self):
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 2, 2)])
        closer.assert_called_once()
        kwargs = closer.call_args.kwargs
        self.assertEqual(kwargs["params"], [[LIVE_RESOURCE], self.OBSERVED_AT])
        self.assertEqual(kwargs["closing_reason"], "workload_recovered")
        self.assertIn("kubernetes_api_server", kwargs["where_conditions"])
        # Config-change findings describe a past event, not a recoverable condition:
        # a healthy workload is their normal state, so recovery must not touch them.
        self.assertIn("finding_type = 'issue'", kwargs["where_conditions"])
        # The close is anchored to when the agent OBSERVED the workload healthy,
        # so an event that started after the snapshot survives it.
        self.assertIn("starts_at < %s", kwargs["where_conditions"])

    def test_close_is_anchored_to_the_oldest_observation(self):
        """A mixed batch must not close on evidence newer than its oldest snapshot."""
        older = self.OBSERVED_AT - timedelta(minutes=20)
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(
                ACCOUNT,
                [
                    self._workload(LIVE_RESOURCE, 1, 1),
                    self._workload(GONE_RESOURCE, 1, 1, last_seen=older.isoformat()),
                ],
            )
        self.assertEqual(closer.call_args.kwargs["params"][1], older)

    def test_timezone_aware_input_is_normalised_to_naive_utc(self):
        """An aware value must not reach a `timestamp` column, and must not break min()."""
        aware = self.OBSERVED_AT.replace(tzinfo=timezone.utc) + timedelta(hours=2)
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(
                ACCOUNT,
                [
                    self._workload(LIVE_RESOURCE, 1, 1),
                    self._workload(GONE_RESOURCE, 1, 1, last_seen=aware.isoformat()),
                ],
            )
        observed = closer.call_args.kwargs["params"][1]
        self.assertIsNone(observed.tzinfo)
        self.assertEqual(observed, self.OBSERVED_AT)

    def test_missing_observation_time_closes_nothing(self):
        """utc_from_epoch_millis(0) means the agent sent no update_time -- fail closed."""
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(
                ACCOUNT,
                [self._workload(LIVE_RESOURCE, 1, 1, last_seen="1970-01-01T00:00:00+00:00")],
            )
        closer.assert_not_called()

    def test_crashlooping_workload_is_not_recovered(self):
        """0/1 pods ready is exactly the crashloop case -- it must stay open."""
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 0)])
        closer.assert_not_called()

    def test_stuck_rollout_is_not_recovered(self):
        """spec.replicas == readyReplicas while a new pod is stuck in ImagePullBackOff.

        The old ReplicaSet keeps the Deployment at 2/2, so the counts say healthy; the
        unready pod row is what says otherwise, and only that workload is held open.
        """
        self.unready_rows = [("default", "web")]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(
                ACCOUNT,
                [
                    self._workload(LIVE_RESOURCE, 2, 2),
                    self._workload(GONE_RESOURCE, 1, 1, name="api"),
                ],
            )
        closer.assert_called_once()
        self.assertEqual(closer.call_args.kwargs["params"][0], [GONE_RESOURCE])
        query, params = self._queries("jsonb_array_length")[0]
        self.assertIn("FROM k8s_pods", query)
        self.assertIn("is_active", query)
        # A pod's ready_pods counts containers and total_pods is always 1, so the
        # comparison has to be against the spec's container count.
        self.assertIn("jsonb_array_length(meta->'config'->'containers')", query)
        self.assertEqual(params[0], ACCOUNT)

    def test_only_unready_workload_closes_nothing(self):
        self.unready_rows = [("default", "web")]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 2, 2)])
        closer.assert_not_called()

    def test_same_name_in_another_namespace_does_not_block(self):
        self.unready_rows = [("staging", "web")]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 2, 2)])
        closer.assert_called_once()

    def test_crashloop_sampled_while_its_container_is_up_is_not_recovered(self):
        """The live-crashloop case: the Deployment reads 1/1 and its pod row reads
        Ready, because the container is in the two seconds it runs before exiting.
        Only the start time says the pod restarted a moment ago."""
        self.pod_rows = [("default", "web", CRASHLOOPING)]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        closer.assert_not_called()

    def test_one_crashlooping_replica_holds_the_workload(self):
        self.pod_rows = [("default", "web", STABLE), ("default", "web", CRASHLOOPING), ("default", "web", STABLE)]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 3, 3)])
        closer.assert_not_called()

    def test_crashloop_in_one_workload_does_not_hold_another(self):
        self.pod_rows = [("default", "web", CRASHLOOPING), ("default", "api", STABLE)]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(
                ACCOUNT,
                [
                    self._workload(LIVE_RESOURCE, 1, 1),
                    self._workload(GONE_RESOURCE, 1, 1, name="api"),
                ],
            )
        closer.assert_called_once()
        self.assertEqual(closer.call_args.kwargs["params"][0], [GONE_RESOURCE])

    def test_crashloop_in_another_namespace_does_not_block(self):
        self.pod_rows = [("staging", "web", CRASHLOOPING), ("default", "web", STABLE)]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        closer.assert_called_once()

    def test_stability_is_measured_against_the_observation_time(self):
        """Started 11 minutes before the agent saw the workload ready: stable, even if
        the request is processed much later."""
        self.pod_rows = [("default", "web", _go_status(_go_container(timedelta(minutes=11), last_reason="Error")))]
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        closer.assert_called_once()

    def test_unreadable_pod_status_holds_and_says_so(self):
        """An agent too old to send container status cannot prove recovery."""
        self.pod_rows = [("default", "web", None)]
        with mock.patch.object(dh, "close_events_with_history") as closer, self.assertLogs(level="WARNING") as logs:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        closer.assert_not_called()
        self.assertIn("Holding recovery close for acct-1: 1 workloads", logs.output[0])

    def test_workload_with_no_stored_pod_is_held(self):
        """A Deployment's 1/1 can arrive before its pods are stored; nothing to judge by yet."""
        self.pod_rows = []
        with mock.patch.object(dh, "close_events_with_history") as closer, self.assertLogs(level="WARNING"):
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        closer.assert_not_called()

    def test_stability_query_reads_the_stored_live_pods(self):
        with mock.patch.object(dh, "close_events_with_history"):
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        query, params = self._queries("status_info")[0]
        self.assertIn("FROM k8s_pods", query)
        # tenant_id + this exact is_active predicate let the planner use the partial
        # index idx_k8s_pods_active_account_namespace instead of scanning every pod
        # the account ever had.
        self.assertIn("tenant_id = %s", query)
        self.assertIn("is_active IS NOT FALSE", query)
        self.assertIn("NOT IN ('completed', 'succeeded', 'failed')", query)
        self.assertEqual(params, [ACCOUNT, TENANT, ["default"], ["web"]])

    def test_close_reads_pods_stored_by_the_same_request(self):
        """A snapshot carrying pods and workloads together must judge the pods it carries."""
        calls = []
        workload = mock.Mock(tenant_id=TENANT, namespace="default", name="web", total_pods=1, ready_pods=1)
        with mock.patch.object(
            dh.database, "insert_data", side_effect=lambda table, *a, **k: calls.append(table)
        ), mock.patch.object(dh, "asdict", return_value={}), mock.patch.object(
            dh, "_clean_stale_workloads"
        ), mock.patch.object(
            dh, "_deduplicate_workloads_by_identity", return_value=[workload]
        ), mock.patch.object(
            dh, "close_events_for_recovered_workloads", side_effect=lambda *a: calls.append("close")
        ):
            dh.process_services_updates(ACCOUNT, [mock.Mock()], [workload])
        self.assertEqual(calls, ["k8s_pods", "k8s_workloads", "close"])

    def test_scaled_to_zero_is_not_recovered(self):
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 0, 0)])
        closer.assert_not_called()

    def test_flag_off_disables_the_close(self):
        with mock.patch.object(dh, "close_events_with_history") as closer, mock.patch.object(
            dh.Configs, "EVENT_CLOSE_ON_WORKLOAD_RECOVERY", False
        ):
            dh.close_events_for_recovered_workloads(ACCOUNT, [self._workload(LIVE_RESOURCE, 1, 1)])
        closer.assert_not_called()

    def test_flag_is_on_by_default(self):
        """Pins the deliberate default. Flipping it is a product decision with a
        measured false-close rate on both sides -- off means agent findings never
        resolve, on means some live crashloops close early -- so neither value may
        change silently as a side effect of another edit."""
        from config import Settings

        self.assertTrue(Settings.model_fields["EVENT_CLOSE_ON_WORKLOAD_RECOVERY"].default)


class TestPodIsStable(unittest.TestCase):
    """How long a pod's containers have been running, from its stored status."""

    def _stable(self, status_info):
        return dh._pod_is_stable(status_info, OBSERVED_AT)

    def test_windows_match_the_agent(self):
        """10 minutes is the agent's podCrashLoopStableWindow; an hour is the OOM
        matcher's rate limit. Diverging from the agent closes alerts it will not
        re-raise, so a change here has to be made there too."""
        self.assertEqual(dh.RECOVERY_STABLE_WINDOW, timedelta(minutes=10))
        self.assertEqual(dh.RECOVERY_STABLE_WINDOW_AFTER_OOM, timedelta(minutes=60))

    def test_running_past_the_window_is_stable(self):
        self.assertTrue(self._stable(_go_status(_go_container(timedelta(minutes=11), last_reason="Error"))))

    def test_restarted_inside_the_window_is_not(self):
        self.assertFalse(self._stable(CRASHLOOPING))
        self.assertFalse(self._stable(_go_status(_go_container(timedelta(minutes=9), last_reason="Error"))))

    def test_a_new_pod_has_to_earn_it_too(self):
        """A rollout's first pod has no restarts yet but may be about to crash: the
        agent keeps its rate limit armed until it has run 10 minutes, so closing
        sooner would leave a broken rollout with no alert for up to an hour."""
        self.assertFalse(self._stable(_go_status(_go_container(timedelta(seconds=2)))))
        self.assertTrue(self._stable(_go_status(_go_container(timedelta(minutes=11)))))

    def test_waiting_in_crashloopbackoff_is_not(self):
        waiting = {
            "name": "app",
            "ready": False,
            "state": {"waiting": {"reason": "CrashLoopBackOff"}},
            "lastState": {"terminated": {"reason": "Error", "finishedAt": "2026-08-19T08:29:50Z"}},
        }
        self.assertFalse(self._stable(_go_status(waiting)))

    def test_running_but_not_ready_is_not(self):
        self.assertFalse(self._stable(_go_status(_go_container(timedelta(hours=1), ready=False))))

    def test_every_container_must_qualify(self):
        """A restarting sidecar is a restarting pod."""
        status = _go_status(
            _go_container(timedelta(hours=1)),
            _go_container(timedelta(minutes=1), last_reason="Error", name="sidecar"),
        )
        self.assertFalse(self._stable(status))

    def test_after_an_oom_kill_waits_an_hour(self):
        self.assertFalse(self._stable(_go_status(_go_container(timedelta(minutes=30), last_reason="OOMKilled"))))
        self.assertTrue(self._stable(_go_status(_go_container(timedelta(minutes=61), last_reason="OOMKilled"))))

    def test_python_agent_shape(self):
        self.assertFalse(self._stable(_python_status(timedelta(seconds=2), last_reason="Error")))
        self.assertTrue(self._stable(_python_status(timedelta(minutes=11), last_reason="Error")))
        self.assertFalse(self._stable(_python_status(timedelta(minutes=30), last_reason="OOMKilled")))
        self.assertFalse(self._stable(_python_status(timedelta(hours=1), ready=False)))

    def test_init_containers_are_not_inspected(self):
        """They exit in a healthy pod; one crashlooping keeps the app containers from running."""
        status = _go_status(_go_container(timedelta(hours=1)))
        status["initContainerStatuses"] = [_go_container(timedelta(minutes=1), ready=False, last_reason="Error")]
        self.assertTrue(self._stable(status))

    def test_unknown_when_the_status_cannot_answer(self):
        unreadable_start = _go_status(_go_container(timedelta(hours=1)))
        unreadable_start["containerStatuses"][0]["state"]["running"]["startedAt"] = "not-a-time"
        for status_info in (
            None,
            {},
            {"containerStatuses": []},
            # A Pending pod as the Python agent stores it.
            {"phase": "Pending", "container_statuses": None},
            {"containerStatuses": ["app"]},
            unreadable_start,
        ):
            with self.subTest(status_info=status_info):
                self.assertIsNone(self._stable(status_info))


class TestLegacyStatusUpdate(unittest.TestCase):
    def test_running_is_not_treated_as_resolved(self):
        """phase=Running is what a CrashLoopBackOff pod reports between restarts."""
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.run_status_update([{"service_key": "ns/app", "new_status": "Running"}], TENANT, ACCOUNT)
        closer.assert_not_called()

    def test_succeeded_still_resolves(self):
        with mock.patch.object(dh, "close_events_with_history") as closer:
            dh.run_status_update([{"service_key": "ns/app", "new_status": "Succeeded"}], TENANT, ACCOUNT)
        closer.assert_called_once()


if __name__ == "__main__":
    unittest.main()


class TestDeletedResourceClose(unittest.TestCase):
    """process_deleted_resources must close the deleted resource's events.

    Regression cover for #38238: the close matched on `service_key` alone, which the
    Go agent's discovery writes as `<ns>/<Kind>/<name>` while the agent's own findings
    write `<ns>/<name>`. The two never matched, so a deleted workload kept its alerts
    open forever -- 2,288 of them on production against 1,509 resources that no longer
    existed, and only 5 `resource_deleted` closes in the lifetime of the table.
    """

    # `_id` = uuid5(service_key + account), written as both cloud_resourses.id and
    # external_resource_id by process_service_discovery.
    GONE_ID = "b0000000-0000-0000-0000-00000000dead"
    # What discovery calls the resource...
    DISCOVERY_KEY = "ns/Deployment/report-worker"

    def _run(self, deleted):
        rec = _Recorder([])
        with mock.patch.object(dh.database, "run_query", side_effect=rec), mock.patch.object(
            dh, "close_events_with_history"
        ) as closer:
            dh.process_deleted_resources(ACCOUNT, deleted, TENANT)
        return rec, closer

    def test_close_is_keyed_on_the_resource_id(self):
        """The id arm is what makes the close work across the two key formats."""
        _, closer = self._run({self.GONE_ID: self.DISCOVERY_KEY})
        closer.assert_called_once()
        kwargs = closer.call_args.kwargs
        self.assertIn("cloud_resource_id = ANY(%s::uuid[])", kwargs["where_conditions"])
        self.assertEqual(kwargs["params"][0], [self.GONE_ID])
        self.assertEqual(kwargs["closing_reason"], "resource_deleted")

    def test_service_key_arm_is_retained_for_robusta_clusters(self):
        """Robusta writes both sides in the same format; that arm still closes there."""
        _, closer = self._run({self.GONE_ID: self.DISCOVERY_KEY})
        kwargs = closer.call_args.kwargs
        self.assertIn("service_key = ANY(%s)", kwargs["where_conditions"])
        self.assertEqual(kwargs["params"][1], [self.DISCOVERY_KEY])

    def test_close_uses_the_same_ids_as_the_inventory_updates(self):
        """The ids are already correct here -- k8s_pods/k8s_workloads key on them."""
        rec, closer = self._run({self.GONE_ID: self.DISCOVERY_KEY})
        inventory_params = [
            values for stmt, values in rec.queries if "UPDATE k8s_pods" in stmt or "UPDATE k8s_workloads" in stmt
        ]
        self.assertEqual(len(inventory_params), 2)
        for values in inventory_params:
            self.assertEqual(values[-1], closer.call_args.kwargs["params"][0])

    def test_nothing_deleted_touches_nothing(self):
        rec, closer = self._run({})
        closer.assert_not_called()
        self.assertEqual(rec.queries, [])

    def test_emitted_sql_binds_params_in_predicate_order(self):
        """Guards the ordering between the two %s placeholders and params."""
        rec = _Recorder([])
        with mock.patch.object(dh.database, "run_query", side_effect=rec), mock.patch.object(
            dh.database, "insert_data"
        ):
            dh.process_deleted_resources(ACCOUNT, {self.GONE_ID: self.DISCOVERY_KEY}, TENANT)

        updates = [(q, v) for q, v in rec.queries if "UPDATE events SET status = 'CLOSED'" in q]
        self.assertEqual(len(updates), 1)
        statement, values = updates[0]
        self.assertIn(
            "WHERE cloud_account_id = %s AND (cloud_resource_id = ANY(%s::uuid[]) "
            "OR service_key = ANY(%s)) AND status != 'CLOSED'",
            statement,
        )
        self.assertEqual(values, [ACCOUNT, [self.GONE_ID], [self.DISCOVERY_KEY]])
