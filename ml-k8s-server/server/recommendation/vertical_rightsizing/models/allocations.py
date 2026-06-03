from __future__ import annotations

import enum
import math
from typing import Literal, Optional, TypeVar, Union, Any

import pydantic as pd

from server.recommendation.vertical_rightsizing.services import resource_units


class ResourceType(str, enum.Enum):
    """The type of resource.

    Just add new types here and they will be automatically supported.
    """

    CPU = "cpu"
    Memory = "memory"


RecommendationValue = Union[float, Literal["?"], None]
RecommendationValueRaw = Union[float, str, None]

Self = TypeVar("Self", bound="ResourceAllocations")

NONE_LITERAL = "unset"
NAN_LITERAL = "?"


def format_recommendation_value(value: RecommendationValue) -> str:
    if value is None:
        return NONE_LITERAL
    elif isinstance(value, str):
        return NAN_LITERAL
    else:
        return resource_units.format(value)


def format_diff(allocated, recommended, selector, multiplier=1, colored=False) -> str:
    if recommended is None or isinstance(recommended.value, str) or selector != "requests":
        return ""
    else:
        reccomended_val = recommended.value if isinstance(recommended.value, (int, float)) else 0
        allocated_val = allocated if isinstance(allocated, (int, float)) else 0
        diff_val = reccomended_val - allocated_val
        if colored:
            diff_sign = "[green]+[/green]" if diff_val >= 0 else "[red]-[/red]"
        else:
            diff_sign = "+" if diff_val >= 0 else "-"
        return f"{diff_sign}{format_recommendation_value(abs(diff_val) * multiplier)}"


class ResourceAllocations(pd.BaseModel):
    requests: dict[ResourceType, RecommendationValue]
    limits: dict[ResourceType, RecommendationValue]
    info: dict[ResourceType, Optional[str]] = {}
    config: dict[str, Any] = {}

    @staticmethod
    def __parse_resource_value(value: RecommendationValueRaw) -> RecommendationValue:
        if value is None:
            return None

        if isinstance(value, str):
            return float(resource_units.parse(value))

        if math.isnan(value):
            return "?"

        return float(value)

    @pd.validator("requests", "limits", pre=True)
    def validate_requests(
        cls, value: dict[ResourceType, RecommendationValueRaw]
    ) -> dict[ResourceType, RecommendationValue]:
        return {
            resource_type: cls.__parse_resource_value(resource_value) for resource_type, resource_value in value.items()
        }
