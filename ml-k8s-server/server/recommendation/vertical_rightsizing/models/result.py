from __future__ import annotations

from typing import Any, Optional, Annotated, Literal

from server.recommendation.vertical_rightsizing.models.allocations import (
    RecommendationValue,
    ResourceAllocations,
    ResourceType,
)
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData
from server.recommendation.vertical_rightsizing.models.severity import Severity
from server.recommendation.vertical_rightsizing.models.config import Config

import numpy as np
import pydantic as pd
from numpy.typing import NDArray

# A type alias for a numpy array of shape (N, 2).
ArrayNx2 = Annotated[NDArray[np.float64], Literal["N", 2]]


PodsTimeData = dict[str, ArrayNx2]  # Mapping: pod -> [(time, value)]
MetricsPodData = dict[str, PodsTimeData]


def scan_severity_to_priority(severity: Severity) -> int:
    if severity == "CRITICAL":
        return 4
    elif severity == "WARNING":
        return 3
    elif severity == "OK":
        return 2
    elif severity == "GOOD":
        return 1
    else:
        return 0


class Recommendation(pd.BaseModel):
    value: RecommendationValue
    severity: Severity

    @property
    def priority(self) -> int:
        return scan_severity_to_priority(self.severity)


class ResourceRecommendation(pd.BaseModel):
    requests: dict[ResourceType, Recommendation]
    limits: dict[ResourceType, Recommendation]
    info: dict[ResourceType, Optional[str]]
    config: dict[str, Any]


RunResult = dict[ResourceType, ResourceRecommendation]


class ResourceScanMetric(pd.BaseModel):
    query: str
    start_time: str
    end_time: str
    step: str


class ResourceScan(pd.BaseModel):
    object: K8sObjectData
    recommended: ResourceRecommendation
    severity: Severity
    metrics: dict[ResourceType, ResourceScanMetric] = {}

    @property
    def priority(self) -> int:
        return scan_severity_to_priority(self.severity)

    @classmethod
    def calculate(cls, object: K8sObjectData, recommendation: ResourceAllocations) -> ResourceScan:
        recommendation_processed = ResourceRecommendation(requests={}, limits={}, info={}, config={})

        for resource_type in ResourceType:
            recommendation_processed.config[resource_type] = recommendation.config.get(resource_type)
            recommendation_processed.info[resource_type] = recommendation.info.get(resource_type)
            for selector in ["requests", "limits"]:
                current = getattr(object.allocations, selector).get(resource_type)
                recommended = getattr(recommendation, selector).get(resource_type)

                current_severity = Severity.calculate(current, recommended, resource_type)

                getattr(recommendation_processed, selector)[resource_type] = Recommendation(
                    value=recommended, severity=current_severity
                )

        for severity in [Severity.CRITICAL, Severity.WARNING, Severity.OK, Severity.GOOD, Severity.UNKNOWN]:
            for selector in ["requests", "limits"]:
                for recommendation_request in getattr(recommendation_processed, selector).values():
                    if recommendation_request.severity == severity:
                        return cls(object=object, recommended=recommendation_processed, severity=severity)

        return cls(object=object, recommended=recommendation_processed, severity=Severity.UNKNOWN)


class StrategyData(pd.BaseModel):
    name: str
    settings: dict[str, Any]


class Result(pd.BaseModel):
    scans: list[ResourceScan]
    score: int = 0
    resources: list[ResourceType] = [ResourceType.CPU, ResourceType.Memory]
    description: Optional[str] = None
    strategy: StrategyData
    errors: list[dict[str, Any]] = pd.Field(default_factory=list)
    clusterSummary: dict[str, Any] = {}
    config: Config

    def __init__(self, *args, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        self.score = self.__calculate_score()

    @staticmethod
    def __scan_cost(scan: ResourceScan) -> float:
        return 0.7 if scan.severity == Severity.WARNING else 1 if scan.severity == Severity.CRITICAL else 0

    def __calculate_score(self) -> int:
        """Get the score of the result.

        Returns:
            The score of the result.
        """

        score = sum(self.__scan_cost(scan) for scan in self.scans)
        return int((len(self.scans) - score) / len(self.scans) * 100) if self.scans else 0

    @property
    def score_letter(self) -> str:
        return (
            "F"
            if self.score < 30
            else "D" if self.score < 55 else "C" if self.score < 70 else "B" if self.score < 90 else "A"
        )
