import pytest

from server.recommendation.cluster_rightsizing_recommendation import ClusterRightSizingRecommendation
from server.resource_models.utils import convert_memory_unit_to_bytes


@pytest.mark.parametrize(
    "parse",
    [
        ClusterRightSizingRecommendation.get_number_of_bytes_from_kubernetes_mem_spec,
        convert_memory_unit_to_bytes,
    ],
)
@pytest.mark.parametrize(
    "quantity, expected",
    [("1k", 1000), ("250k", 250000), ("1Ki", 1024), ("1M", 1000000), ("1Mi", 1048576)],
)
def test_canonical_memory_quantities_use_decimal_and_binary_units(parse, quantity, expected):
    assert parse(quantity) == expected
