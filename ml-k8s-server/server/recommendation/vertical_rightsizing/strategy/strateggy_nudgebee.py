import textwrap
from datetime import datetime
import numpy as np
import pydantic as pd
from typing import Dict, List, Union

from server.recommendation.vertical_rightsizing.strategy.strategies import (
    BaseStrategy,
    K8sObjectData,
    MetricsPodData,
    ResourceRecommendation,
    ResourceType,
    RunResult,
    StrategySettings,
)
from server.recommendation.vertical_rightsizing.metrics.cpu import (
    CPUAmountLoader,
    PercentileCPULoader,
)

from server.recommendation.vertical_rightsizing.metrics.memory import (
    MaxMemoryLoader,
    MemoryAmountLoader,
    MaxOOMKilledMemoryLoader,
)

from server.recommendation.vertical_rightsizing.models.result import PodsTimeData


from server.recommendation.vertical_rightsizing.metrics.base import PrometheusMetric


class NudgebeeStrategySettings(StrategySettings):
    cpu_percentile: float = pd.Field(99, gt=0, le=100, description="The percentile to use for the CPU recommendation.")
    cpu_percentile_99: float = pd.Field(
        99, gt=0, le=100, description="The percentile to use for the CPU recommendation."
    )
    cpu_percentile_97: float = pd.Field(
        97, gt=0, le=100, description="The percentile to use for the CPU recommendation."
    )
    cpu_percentile_95: float = pd.Field(
        95, gt=0, le=100, description="The percentile to use for the CPU recommendation."
    )
    cpu_percentile_92: float = pd.Field(
        92, gt=0, le=100, description="The percentile to use for the CPU recommendation."
    )

    memory_buffer_percentage: float = pd.Field(
        15, gt=0, description="The percentage of added buffer to the peak memory usage for memory recommendation."
    )
    points_required: int = pd.Field(
        50, ge=1, description="The number of data points required to make a recommendation for a resource."
    )
    use_oomkill_data: bool = pd.Field(
        True,
        description="Whether to bump the memory when OOMKills are detected (experimental).",
    )
    oom_memory_buffer_percentage: float = pd.Field(
        25, gt=0, description="What percentage to increase the memory when there are OOMKill events."
    )
    allow_hpa: bool = pd.Field(
        True,
        description="Whether to calculate recommendations even when there is an HPA scaler defined on that resource.",
    )

    def calculate_memory_proposal(self, data: PodsTimeData, max_oomkill: float = 0) -> float:
        data_ = [np.max(values[:, 1]) for values in data.values()]
        if len(data_) == 0:
            return float("NaN")

        return np.max(data_), max(
            np.max(data_) * (1 + self.memory_buffer_percentage / 100),
            max_oomkill * (1 + self.oom_memory_buffer_percentage / 100),
        )

    def calculate_cpu_proposal(self, data: Dict[str, PodsTimeData]) -> Dict[str, float]:
        result = {}
        for percentile_name, per_data in data.items():
            if len(per_data) == 0:
                result[percentile_name] = float("NaN")

            if len(per_data) > 1:
                data_ = np.concatenate([values[:, 1] for values in per_data.values()])
            else:
                data_ = list(per_data.values())[0][:, 1]
            result[percentile_name] = np.max(data_)
        return result


class NudgebeeStrategy(BaseStrategy[NudgebeeStrategySettings]):
    """
    CPU request: {cpu_percentile}% percentile, limit: unset
    Memory request: max + {memory_buffer_percentage}%, limit: max + {memory_buffer_percentage}%
    History: {history_duration} hours
    Step: {timeframe_duration} minutes

    This strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).
    If HPA is defined for CPU or Memory, the strategy will return "?" for that resource.

    Learn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]
    """

    display_name = "nudgebee"
    rich_console = False

    @property
    def description(self):
        s = textwrap.dedent(
            """
            CPU request: {cpu_percentile}% percentile, limit: unset
            Memory request: max + {memory_buffer_percentage}%, limit: max + {memory_buffer_percentage}%
            History: {history_duration} hours
            Step: {timeframe_duration} minutes

            All parameters can be customized. For example:
            `krr simple --cpu_percentile=90 --memory_buffer_percentage=15
                --history_duration=24
                --timeframe_duration=0.5
            `
            """.format(
                cpu_percentile=self.settings.cpu_percentile,
                memory_buffer_percentage=self.settings.memory_buffer_percentage,
                history_duration=self.settings.history_duration,
                timeframe_duration=self.settings.timeframe_duration,
            )
        )
        return s

    @property
    def metrics(self) -> Union[list[type[PrometheusMetric]], dict[str, list[type[PrometheusMetric]]]]:
        metrics = {
            "cpu_percentile_97": PercentileCPULoader(self.settings.cpu_percentile_97),
            "cpu_percentile_95": PercentileCPULoader(self.settings.cpu_percentile_95),
            "cpu_percentile_99": PercentileCPULoader(self.settings.cpu_percentile_99),
            "cpu_percentile_92": PercentileCPULoader(self.settings.cpu_percentile_92),
            "MaxMemoryLoader": MaxMemoryLoader,
            "CPUAmountLoader": CPUAmountLoader,
            "MemoryAmountLoader": MemoryAmountLoader,
        }
        if self.settings.use_oomkill_data:
            metrics["MaxOOMKilledMemoryLoader"] = MaxOOMKilledMemoryLoader
        return metrics

    def __calculate_cpu_proposal(
        self, history_data: MetricsPodData, object_data: K8sObjectData
    ) -> ResourceRecommendation:
        data = {
            "cpu_percentile_97": history_data["cpu_percentile_97"],
            "cpu_percentile_95": history_data["cpu_percentile_95"],
            "cpu_percentile_99": history_data["cpu_percentile_99"],
            "cpu_percentile_92": history_data["cpu_percentile_92"],
        }

        if len(data["cpu_percentile_99"]) == 0:
            return ResourceRecommendation.undefined(info="No data")

        data_count = {pod: values[0, 1] for pod, values in history_data["CPUAmountLoader"].items()}
        # Here we filter out pods from calculation that have less than `points_required` data points
        filtered_data = {}
        for percentile, percentile_data in data.items():
            filt_data = {
                pod: values
                for pod, values in percentile_data.items()
                if data_count.get(pod, 0) >= self.settings.points_required
                or (object_data.kind == "Job" or object_data.kind == "CronJob")
            }
            filtered_data[percentile] = filt_data
            if len(filt_data) == 0:
                return ResourceRecommendation.undefined(info="Not enough data")

            if (
                object_data.hpa is not None
                and object_data.hpa.target_cpu_utilization_percentage is not None
                and not self.settings.allow_hpa
            ):
                return ResourceRecommendation.undefined(info="HPA detected")

        cpu_usage = self.settings.calculate_cpu_proposal(filtered_data)

        return ResourceRecommendation(request=cpu_usage["cpu_percentile_99"], limit=None, config=cpu_usage)

    def _extract_oom_details(self, history_data: MetricsPodData) -> Dict:
        """
        Extract detailed OOM kill event information from history data.

        Returns a dictionary with:
        - oomkill_detected: bool
        - oom_event_count: int (number of pods affected by OOM)
        - oom_affected_pods: list of pod names that experienced OOM
        - oom_memory_limit_at_kill: memory limit in bytes when OOM occurred
        - oom_last_seen_timestamp: ISO timestamp of the OOM data point (approximate)
        """
        oom_details: Dict = {
            "oomkill_detected": False,
            "oom_event_count": 0,
            "oom_affected_pods": [],
            "oom_memory_limit_at_kill": None,
            "oom_last_seen_timestamp": None,
        }

        if not self.settings.use_oomkill_data:
            return oom_details

        max_oomkill_data = history_data.get("MaxOOMKilledMemoryLoader", {})

        if len(max_oomkill_data) == 0:
            return oom_details

        # Extract information from MaxOOMKilledMemoryLoader
        # Data format: dict[pod_name, numpy array of shape (N, 2) where each row is [timestamp, value]]
        affected_pods: List[str] = []
        max_memory_limit = 0.0
        latest_timestamp = 0.0

        for pod_name, values in max_oomkill_data.items():
            if len(values) > 0:
                # values[0] is the first (and typically only) data point: [timestamp, memory_limit]
                timestamp, memory_limit = float(values[0, 0]), float(values[0, 1])
                if memory_limit > 0:
                    affected_pods.append(pod_name)
                    max_memory_limit = max(max_memory_limit, memory_limit)
                    latest_timestamp = max(latest_timestamp, timestamp)

        if max_memory_limit == 0:
            return oom_details

        oom_details["oomkill_detected"] = True
        oom_details["oom_event_count"] = len(affected_pods)
        oom_details["oom_affected_pods"] = affected_pods
        oom_details["oom_memory_limit_at_kill"] = max_memory_limit

        if latest_timestamp > 0:
            oom_details["oom_last_seen_timestamp"] = datetime.utcfromtimestamp(latest_timestamp).isoformat() + "Z"

        return oom_details

    def __calculate_memory_proposal(
        self, history_data: MetricsPodData, object_data: K8sObjectData
    ) -> ResourceRecommendation:
        data = history_data["MaxMemoryLoader"]

        if len(data) == 0:
            return ResourceRecommendation.undefined(info="No data")

        # Extract detailed OOM information
        oom_details = self._extract_oom_details(history_data)

        # Get max OOM kill value for memory calculation
        max_oomkill_value = 0.0
        if self.settings.use_oomkill_data:
            max_oomkill_data = history_data.get("MaxOOMKilledMemoryLoader", {})
            if len(max_oomkill_data) > 0:
                max_oomkill_value = float(np.max([values[0, 1] for values in max_oomkill_data.values()]))

        data_count = {pod: values[0, 1] for pod, values in history_data["MemoryAmountLoader"].items()}
        # Here we filter out pods from calculation that have less than `points_required` data points
        filtered_data = {
            pod: value for pod, value in data.items() if data_count.get(pod, 0) >= self.settings.points_required
        }

        if len(filtered_data) == 0:
            return ResourceRecommendation.undefined(info="Not enough data")

        if (
            object_data.hpa is not None
            and object_data.hpa.target_memory_utilization_percentage is not None
            and not self.settings.allow_hpa
        ):
            return ResourceRecommendation.undefined(info="HPA detected")

        actual_recomm_memory_usage, memory_usage_beffered = self.settings.calculate_memory_proposal(
            filtered_data, max_oomkill_value
        )

        # Build config with detailed OOM information
        config = {
            "actual_recommended_request": actual_recomm_memory_usage,
            "actual_recommended_limit": actual_recomm_memory_usage,
            **oom_details,  # Include all OOM details
        }

        return ResourceRecommendation(request=memory_usage_beffered, limit=memory_usage_beffered, config=config)

    def run(self, history_data: MetricsPodData, object_data: K8sObjectData) -> RunResult:
        result = {
            ResourceType.CPU: self.__calculate_cpu_proposal(history_data, object_data),
            ResourceType.Memory: self.__calculate_memory_proposal(history_data, object_data),
        }
        return result
