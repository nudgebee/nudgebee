from __future__ import annotations

import abc
import datetime
from typing import Generic, Optional, Sequence, TypeVar, Dict, Any

import pydantic as pd
from server.recommendation.vertical_rightsizing.models.result import K8sObjectData, ResourceType
from server.recommendation.vertical_rightsizing.metrics.base import PrometheusMetric
from server.recommendation.vertical_rightsizing.models.result import MetricsPodData, RunResult

SelfRR = TypeVar("SelfRR", bound="ResourceRecommendation")


class ResourceRecommendation(pd.BaseModel):
    """A class to represent resource recommendation with optional request and limit values.

    The NaN values are used to represent undefined values:
        the strategy did not provide a recommendation for the resource.
    None values are used to represent the strategy says that value should not be set.
    """

    request: Optional[float]
    limit: Optional[float]
    info: Optional[str] = pd.Field(None, description="Additional information about the recommendation.")
    config: Optional[Dict[str, Any]] = {}

    @classmethod
    def undefined(cls: type[SelfRR], info: Optional[str] = None) -> SelfRR:
        return cls(request=float("NaN"), limit=float("NaN"), info=info)


class StrategySettings(pd.BaseModel):
    """A class to represent strategy settings with configurable history and timeframe duration.

    It is used in CLI to generate the help, parameters and validate values.
    Description is used to generate the help.
    Other pydantic features can be used to validate the values.

    Nested classes are not supported here.
    """

    history_duration: float = pd.Field(
        24 * 7 * 2, ge=1, description="The duration of the history data to use (in hours)."
    )
    timeframe_duration: float = pd.Field(1.25, gt=0, description="The step for the history data (in minutes).")

    @property
    def history_timedelta(self) -> datetime.timedelta:
        return datetime.timedelta(hours=self.history_duration)

    @property
    def timeframe_timedelta(self) -> datetime.timedelta:
        return datetime.timedelta(minutes=self.timeframe_duration)

    def history_range_enough(self, history_range: tuple[datetime.timedelta, datetime.timedelta]) -> bool:
        """Override this function to check if the history range is enough for the strategy."""

        return True


SelfBS = TypeVar("SelfBS", bound="BaseStrategy")
_StrategySettings = TypeVar("_StrategySettings", bound=StrategySettings)


# An abstract base class for strategy implementation.
# This class requires implementation of a 'run' method for calculating recommendation.
# Make a subclass if you want to create a concrete strategy.
class BaseStrategy(abc.ABC, Generic[_StrategySettings]):
    """An abstract base class for strategy implementation.

    This class is generic, and requires a type for the settings.
    This settings type will be used for the settings property of the strategy.
    It will be used to generate CLI parameters for this strategy, validated by pydantic.

    This class requires implementation of a 'run' method for calculating recommendation.
    Additionally, it provides a 'description' property for generating a description for the strategy.
    Description property uses the docstring of the strategy class and the settings of the strategy.

    The name of the strategy is the name of the class in lowercase, without the 'Strategy' suffix, if exists.
    If you want to change the name of the strategy, you can change the display_name class attribute.

    The strategy will automatically be registered in the strategy registry using __subclasses__ mechanism.
    """

    display_name: str
    rich_console: bool = False

    # TODO: this should be BaseMetric, but currently we only support Prometheus
    @property
    @abc.abstractmethod
    def metrics(self) -> Sequence[type[PrometheusMetric]]:
        pass

    def __init__(self, settings: _StrategySettings):
        self.settings = settings

    def __str__(self) -> str:
        return self.display_name.title()

    @property
    def description(self) -> Optional[str]:
        """
        Generate a description for the strategy.
        You can use Rich's markdown syntax to format the description.
        """
        raise NotImplementedError()

    # Abstract method that needs to be implemented by subclass.
    # This method is intended to calculate resource recommendation based on history data and kubernetes object data.
    @abc.abstractmethod
    def run(self, history_data: MetricsPodData, object_data: K8sObjectData) -> RunResult:
        pass


AnyStrategy = BaseStrategy[StrategySettings]


__all__ = [
    "AnyStrategy",
    "BaseStrategy",
    "StrategySettings",
    "MetricsPodData",
    "K8sObjectData",
    "ResourceType",
]
