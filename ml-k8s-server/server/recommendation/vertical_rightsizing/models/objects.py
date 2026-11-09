from __future__ import annotations

from typing import Literal, Optional

import pydantic as pd

from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations
from server.recommendation.vertical_rightsizing.models.replicas import ReplicaStats
from server.recommendation.vertical_rightsizing.services.batched import batched

KindLiteral = Literal["Deployment", "DaemonSet", "StatefulSet", "Job", "CronJob", "Rollout", "DeploymentConfig"]


class PodData(pd.BaseModel):
    name: str
    deleted: bool

    def __hash__(self) -> int:
        return hash(self.name)


class HPAData(pd.BaseModel):
    min_replicas: Optional[int]
    max_replicas: int
    current_replicas: Optional[int]
    desired_replicas: int
    target_cpu_utilization_percentage: Optional[float]
    target_memory_utilization_percentage: Optional[float]


PodWarning = Literal[
    "NoPrometheusPods",
    "NoPrometheusCPUMetrics",
    "NoPrometheusMemoryMetrics",
]


class K8sObjectData(pd.BaseModel):
    # NOTE: Here None means that we are running inside the cluster
    cluster: Optional[str]
    name: str
    container: str
    pods: list[PodData] = []
    hpa: Optional[HPAData]
    namespace: str
    kind: KindLiteral
    allocations: ResourceAllocations
    warnings: set[PodWarning] = set()
    selector: Optional[dict] = None
    # Time-weighted replica count over the observation window; None when the
    # metrics backend cannot supply it, in which case current_pods_count is used.
    replica_stats: Optional[ReplicaStats] = None

    _api_resource = pd.PrivateAttr(None)
    _live_pods_count: Optional[int] = pd.PrivateAttr(None)

    def __str__(self) -> str:
        return f"{self.kind} {self.namespace}/{self.name}/{self.container}"

    def __hash__(self) -> int:
        return hash(str(self))

    def add_warning(self, warning: PodWarning) -> None:
        self.warnings.add(warning)

    @property
    def current_pods_count(self) -> int:
        if self._live_pods_count is not None:
            return self._live_pods_count
        return len([pod for pod in self.pods if not pod.deleted])

    def release_pods(self) -> None:
        """Drop the pod list once the strategy has run, keeping only the live count.

        The list holds every pod the workload had over the history window, and a scan
        keeps every workload until it ends, so it is most of what a scan retains.
        """
        self._live_pods_count = self.current_pods_count
        self.pods = []

    @property
    def deleted_pods_count(self) -> int:
        return len([pod for pod in self.pods if pod.deleted])

    @property
    def pods_count(self) -> int:
        return len(self.pods)

    def split_into_batches(self, n: int) -> list[K8sObjectData]:
        """
        Batch this object into n objects, splitting the pods into batches of size n.
        """

        if self.pods_count <= n:
            return [self]

        return [
            K8sObjectData(
                cluster=self.cluster,
                name=self.name,
                container=self.container,
                pods=batch,
                hpa=self.hpa,
                namespace=self.namespace,
                kind=self.kind,
                allocations=self.allocations,
            )
            for batch in batched(self.pods, n)
        ]
