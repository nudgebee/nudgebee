"""Datadog and Elasticsearch rightsizing queries travel through api-server.

api-server resolves the account's integration and its credentials, so no key or
password reaches this service.

These lock the two things a unit test can still check: that each service asks
api-server for its own provider's dialect, and that the response it gets back is read
correctly.
"""

import asyncio
import json
from datetime import timedelta
from unittest.mock import patch

import pytest

from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import DatadogMetricsService
from server.recommendation.vertical_rightsizing.services.metrics_elasticsearch_service import (
    ElasticsearchMetricsService,
    pod_aggregates,
)


class _Strategy:
    def __init__(self, metrics):
        self.metrics = metrics


def _object():
    return K8sObjectData(
        cluster=None,
        name="app",
        container="app",
        namespace="ns",
        kind="Deployment",
        hpa=None,
        pods=[PodData(name="app-abc-1", deleted=False)],
        allocations=ResourceAllocations(requests={}, limits={}),
    )


class TestDatadogThroughApiServer:
    def test_the_query_goes_to_api_server_as_datadog(self):
        service = DatadogMetricsService(config=None, account_id="acct", executor=None, tenant_id="tenant")

        with patch(
            "server.recommendation.vertical_rightsizing.services.metrics_datadog_service."
            "query_metrics_via_api_server",
            return_value=[],
        ) as sent:
            service._query_datadog("avg:kubernetes.cpu.usage.total{kube_namespace:ns}", 1_700_000_000, 1_700_086_400)

        kwargs = sent.call_args.kwargs
        assert kwargs["metric_provider"] == "datadog"
        assert kwargs["query"].startswith("avg:kubernetes.cpu.usage.total")
        assert kwargs["account_id"] == "acct" and kwargs["tenant_id"] == "tenant"
        # The window travels as datetimes; api-server takes it from there.
        assert kwargs["start_time"].timestamp() == pytest.approx(1_700_000_000)
        assert kwargs["end_time"].timestamp() == pytest.approx(1_700_086_400)

    def test_api_server_series_are_read_by_pod_and_container(self):
        from_api_server = [
            {
                "metric": {"pod_name": "app-abc-1", "kube_container_name": "app"},
                "timestamps": [1_700_000_000],
                # api-server formats values as strings and drops the null, keeping the
                # arrays aligned.
                "values": ["1500000"],
            }
        ]

        service = DatadogMetricsService(config=None, account_id="acct", executor=None, tenant_id="t")
        parsed = service._parse_series_by_pod_container(from_api_server)
        assert parsed["app"]["app-abc-1"] == [(1_700_000_000.0, 1_500_000.0)]


class TestElasticsearchThroughApiServer:
    def test_the_body_goes_to_api_server_as_a_dsl_query(self):
        service = ElasticsearchMetricsService(config=None, account_id="acct", executor=None, tenant_id="tenant")

        with patch(
            "server.recommendation.vertical_rightsizing.services.metrics_elasticsearch_service."
            "query_metrics_via_api_server",
            return_value=[],
        ) as sent:
            asyncio.run(service.load_pods(_object(), timedelta(days=14)))

        kwargs = sent.call_args.kwargs
        assert kwargs["metric_provider"] == "ES"
        assert kwargs["request"] == {"query_type": "dsl", "batch": True}
        body = json.loads(kwargs["query"])
        assert body["aggs"]["pods"]["terms"]["field"] == "kubernetes.pod.name"
        # api-server ANDs its own range clause onto the body, so the window it is given
        # has to be the one the body already filters on.
        window = [f for f in body["query"]["bool"]["filter"] if "range" in f][0]["range"]["@timestamp"]
        assert kwargs["start_time"].isoformat() == window["gte"]
        assert kwargs["end_time"].isoformat() == window["lte"]

    def test_api_server_values_are_read_per_pod(self):
        from_api_server = [
            {"metric": {"pods": "app-abc-1", "__name__": "count"}, "timestamps": [0], "values": ["120"]},
            {
                "metric": {"pods": "app-abc-1", "__name__": "last_seen"},
                "timestamps": [0],
                "values": ["1787811900000"],
            },
            {
                "metric": {"pods": "app-abc-1", "__name__": "pct", "statistic": "99.0"},
                "timestamps": [0],
                "values": ["2500000000"],
            },
            {
                "metric": {"pods": "app-abc-1", "__name__": "pct", "statistic": "95.0"},
                "timestamps": [0],
                "values": ["1000000000"],
            },
        ]

        agg = pod_aggregates(from_api_server)["app-abc-1"]
        assert agg.percentile(99) == pytest.approx(2_500_000_000.0)
        # The caller asks with "95"; Elasticsearch keys it "95.0".
        assert agg.percentile(95) == pytest.approx(1_000_000_000.0)
        assert agg.count == pytest.approx(120)
        assert agg.timestamp == pytest.approx(1_787_811_900.0), "epoch millis become seconds"

    def test_a_pod_with_no_last_seen_is_stamped_rather_than_dropped(self):
        aggs = pod_aggregates(
            [{"metric": {"pods": "app-abc-1", "__name__": "count"}, "timestamps": [0], "values": ["5"]}]
        )
        assert aggs["app-abc-1"].timestamp > 0


def _claim_map(svc, series):
    """Run one claim key through the service, answering with `series`."""
    import asyncio
    from datetime import timedelta
    from unittest.mock import patch

    with patch(
        "server.recommendation.volume_rightsizing.query_utilisation_via_api_server",
        return_value={"pvc_used_by_claim": series},
    ):
        return asyncio.run(svc._query_claim_metric("pvc_used_by_claim", timedelta(minutes=10)))


class TestUnreadableReadingsAreSkippedNotFatal:
    """One unreadable point must not cost a claim its usage or a workload its scan.

    Raised in review of #38823: the volume scan walks a series backwards for the most
    recent usable reading, and gave up on the whole claim when the first one it tried
    would not parse — which reads downstream as "this claim has no usage" and shrinks
    or drops it.
    """

    def test_volume_usage_falls_back_to_an_older_readable_value(self):
        from unittest.mock import MagicMock, patch

        from server.recommendation.volume_rightsizing import VolumeRightsizingService

        with patch(
            "server.recommendation.volume_rightsizing.DatabaseEngine.get_engine",
            return_value=MagicMock(),
        ):
            svc = VolumeRightsizingService(account_id="acct", tenant_id="tenant")

        usage = _claim_map(
            svc,
            [
                {
                    "metric": {"persistentvolumeclaim": "data-loki-0", "namespace": "loki"},
                    # Newest last. The newest reading is unreadable, the one before it is good.
                    "values": [7_000_000_000.0, 8_000_000_000.0, "not-a-number"],
                }
            ],
        )
        assert usage == {"data-loki-0_loki": pytest.approx(8_000_000_000.0)}

    def test_a_nan_reading_is_skipped_like_an_unreadable_one(self):
        """`float("NaN")` parses happily, unlike "nope".

        A NaN usage compares false against every threshold downstream, so the claim is
        sized as though it were empty — a full volume reported as having room. The walk
        keeps going back for a real reading instead.
        """
        from unittest.mock import MagicMock, patch

        from server.recommendation.volume_rightsizing import VolumeRightsizingService

        with patch(
            "server.recommendation.volume_rightsizing.DatabaseEngine.get_engine",
            return_value=MagicMock(),
        ):
            svc = VolumeRightsizingService(account_id="acct", tenant_id="tenant")

        usage = _claim_map(
            svc,
            [
                {
                    "metric": {"persistentvolumeclaim": "data-loki-0", "namespace": "loki"},
                    # Newest last: NaN, then Inf, then a real reading before them.
                    "values": [8_000_000_000.0, "Inf", "NaN"],
                }
            ],
        )
        assert usage == {"data-loki-0_loki": pytest.approx(8_000_000_000.0)}

    def test_a_series_of_only_unreadable_values_yields_no_entry(self):
        from unittest.mock import MagicMock, patch

        from server.recommendation.volume_rightsizing import VolumeRightsizingService

        with patch(
            "server.recommendation.volume_rightsizing.DatabaseEngine.get_engine",
            return_value=MagicMock(),
        ):
            svc = VolumeRightsizingService(account_id="acct", tenant_id="tenant")

        usage = _claim_map(
            svc,
            [
                {
                    "metric": {"persistentvolumeclaim": "data-loki-0", "namespace": "loki"},
                    "values": ["nope", None, "also-nope"],
                }
            ],
        )
        assert usage == {}, "no usage is the honest answer; a zero would shrink the claim"

    def test_liveness_ignores_an_unreadable_timestamp(self):
        from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import (
            pods_from_series,
        )

        pods = {
            p.name: p
            for p in pods_from_series(
                [
                    {"metric": {"pod_name": "web-1"}, "timestamps": ["bad", 1_000_500.0]},
                    {"metric": {"pod_name": "web-2"}, "timestamps": ["bad"]},
                ],
                live_cutoff=1_000_000.0,
            )
        }
        assert pods["web-1"].deleted is False, "the readable point still decides liveness"
        assert "web-2" not in pods, "a series with nothing readable carries no liveness signal"

    def test_an_unreadable_sample_is_dropped_like_a_gap(self):
        from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import (
            DatadogMetricsService,
        )

        svc = DatadogMetricsService(config=None, account_id="acct", executor=None, tenant_id="t")
        parsed = svc._parse_series_by_pod_container(
            [
                {
                    "metric": {"pod_name": "web-1", "kube_container_name": "web"},
                    "timestamps": [1_700_000_000, 1_700_000_060],
                    "values": ["oops", "1500000"],
                }
            ]
        )
        assert parsed["web"]["web-1"] == [(1_700_000_060.0, 1_500_000.0)]


class TestTheDataFloorMeansTheSameThingOnEveryProvider:
    """points_required is 50 steps of 75s — about an hour of observed data.

    Datadog scales its rollup with the window: measured against the dev account, every
    delta in a 14-day query is exactly 7200s, while a 24-hour query comes back in
    5-minute buckets. Counting readings directly therefore turned that one-hour floor
    into a four-day one, and every pod on a cluster that redeploys often fell under it:
    400 pod incarnations over 14 days, the busiest with 9 readings, none reaching 50.
    """

    def test_hourly_buckets_count_as_the_time_they_cover(self):
        from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import (
            bucket_seconds,
            equivalent_points,
        )
        import numpy as np

        # What dev returned: 2-hour buckets, one pod alive for 8 of them.
        bucket = 7200.0
        arrays = {
            "web-1": np.array([[1_700_000_000 + i * bucket, 1.0] for i in range(8)]),
            "web-2": np.array([[1_700_000_000 + i * bucket, 1.0] for i in range(3)]),
        }
        measured = bucket_seconds(arrays)
        assert measured == pytest.approx(bucket)

        # 8 readings cover 16 hours, which is past the one-hour floor.
        assert equivalent_points(arrays["web-1"], measured) == pytest.approx(8 * bucket / 75.0)
        assert equivalent_points(arrays["web-1"], measured) > 50
        # 3 readings still cover 6 hours, and also count.
        assert equivalent_points(arrays["web-2"], measured) > 50

    def test_a_pod_seen_for_minutes_stays_below_the_floor(self):
        from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import (
            equivalent_points,
        )
        import numpy as np

        # A short window, sampled every 30s: two readings are a minute of data.
        arr = np.array([[1_700_000_000, 1.0], [1_700_000_030, 1.0]])
        assert equivalent_points(arr, 30.0) < 50

    def test_gaps_do_not_stretch_the_bucket(self):
        from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import (
            bucket_seconds,
        )
        import numpy as np

        # An interval a pod did not exist in is absent, not null, so the series has a
        # jump in it. The median ignores it; a mean would follow it and overstate
        # every pod's coverage.
        arr = np.array(
            [[0, 1.0], [300, 1.0], [600, 1.0], [86_400, 1.0], [86_700, 1.0]],
            dtype=float,
        )
        assert bucket_seconds({"web-1": arr}) == pytest.approx(300.0)

    def test_one_reading_with_nothing_to_measure_counts_for_itself(self):
        from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import (
            bucket_seconds,
            equivalent_points,
        )
        import numpy as np

        arr = np.array([[1_700_000_000, 1.0]])
        assert bucket_seconds({"web-1": arr}) == 0.0
        assert equivalent_points(arr, 0.0) == 1.0
