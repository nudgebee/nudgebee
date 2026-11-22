"""Which metrics service a scan gets, and why.

The flag exists because this switch changes where every number in a recommendation
comes from, and the numbers are the product. Turning it off has to put the account
back on exactly the service it had, so both directions are asserted here rather than
only the new one.
"""

from unittest.mock import patch

import pytest

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import DatadogMetricsService
from server.recommendation.vertical_rightsizing.services.metrics_elasticsearch_service import (
    ElasticsearchMetricsService,
)
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import PrometheusMetricsService
from server.recommendation.vertical_rightsizing.services.metrics_utilisation_service import UtilisationMetricsService
from server.recommendation.vertical_rightsizing.services.service import RecommendationService


def _config():
    return Config(
        namespaces=None,
        resources="*",
        selector=None,
        max_workers=1,
        cpu_min_value=10,
        memory_min_value=100,
        strategy="nudgebee",
        other_args={},
    )


def _service(via_utilisation, **kwargs):
    with patch("server.recommendation.vertical_rightsizing.services.service.ApiServerMetricsConfigs") as configs, patch(
        "server.recommendation.vertical_rightsizing.services.nudgebee_cluster_service.ClusterService.__init__",
        return_value=None,
    ):
        configs.rightsizing_via_utilisation = via_utilisation
        service = RecommendationService("acct-1", _config(), tenant_id="tenant-1", **kwargs)
    try:
        yield_service = service._metrics_service_loaders
    finally:
        service.close()
    return yield_service


@pytest.mark.parametrize(
    "metrics_provider, expected",
    [
        (None, PrometheusMetricsService),
        ("prometheus", PrometheusMetricsService),
        ("datadog", DatadogMetricsService),
        ("ES", ElasticsearchMetricsService),
    ],
)
def test_the_flag_off_is_exactly_todays_behaviour(metrics_provider, expected):
    loaders = _service(False, metrics_provider=metrics_provider)
    assert isinstance(loaders, expected)


@pytest.mark.parametrize("metrics_provider", [None, "prometheus", "ES", "newrelic", "dynatrace"])
def test_the_flag_on_is_one_service_for_every_provider_but_datadog(metrics_provider):
    loaders = _service(True, metrics_provider=metrics_provider)
    assert isinstance(loaders, UtilisationMetricsService)


def test_datadog_uses_the_shared_service_like_every_other_provider():
    """Datadog was excluded here until its numbers could be shown to agree.

    They now do, measured against Prometheus on the same cluster at equal resolution:
    memory 100-103%, coverage 100-110%, CPU p99 94-110%. The exclusion existed for
    api-server defects that are fixed (#39288, #39297, #39301) plus two found while
    verifying — a rollup that ignored the window, and coverage counted in buckets
    rather than samples.

    "Every provider but one" was the shape this epic exists to remove, so there is no
    provider name left in this decision at all.
    """
    loaders = _service(True, metrics_provider="datadog")
    assert isinstance(loaders, UtilisationMetricsService)
    assert not isinstance(loaders, DatadogMetricsService)


def test_the_flag_alone_decides_for_every_provider():
    """No provider is named on this path any more."""
    for provider in ("prometheus", "datadog", "ES", "newrelic", "dynatrace", None):
        loaders = _service(True, metrics_provider=provider)
        assert isinstance(loaders, UtilisationMetricsService), f"{provider} should use the shared service"


def test_a_prometheus_family_account_keeps_its_pod_inventory_walk():
    """kube_pod_owner attributes pod incarnations that no longer exist; a pod-name
    prefix does not. Keeping it means the switch changes the numbers and not the
    population they are computed over."""
    loaders = _service(True, metrics_provider="prometheus")
    assert isinstance(loaders._inventory, PrometheusMetricsService)

    # The COMMON case, and the one most easily lost: the trigger names no provider for
    # an agent account, so the field arrives as None rather than "". Failing the
    # membership test here would silently drop the owner-chain walk for the majority of
    # accounts and fall back to deriving pods from a name prefix -- no error, just a
    # different population underneath every recommendation.
    default = _service(True, metrics_provider=None)
    assert isinstance(default._inventory, PrometheusMetricsService)

    es = _service(True, metrics_provider="ES")
    assert es._inventory is None, "Elasticsearch has no owner-chain walk to keep"


def test_the_hint_reaches_the_service_that_forwards_it():
    loaders = _service(
        True,
        metrics_provider="newrelic",
        metric_provider="prometheus",
        metric_provider_source="agent",
    )
    assert loaders.metric_provider == "prometheus"
    assert loaders.metric_provider_source == "agent"


def test_no_hint_means_the_accounts_own_default_resolves():
    loaders = _service(True, metrics_provider="prometheus")
    assert loaders.metric_provider == ""
    assert loaders.metric_provider_source == ""
