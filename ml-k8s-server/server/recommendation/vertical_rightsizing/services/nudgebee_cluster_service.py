import logging
import json
from concurrent.futures import ThreadPoolExecutor

from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations, ResourceType

from server.utils.utils import DatabaseEngine
from sqlalchemy import text

logger = logging.getLogger("krr")


class ClusterService:
    def __init__(self, cluster: str, executor: ThreadPoolExecutor, config: Config):
        self.cluster = cluster
        self._config = config
        self.executor = executor
        try:
            self.engine = DatabaseEngine.get_engine()
        except Exception as e:
            msg = f"Failed to create engine : {e}"
            logger.error(msg)
            raise ValueError(msg)

    async def list_scannable_objects(self) -> list[K8sObjectData]:
        """List all scannable objects.

        Returns:
            A list of scannable objects.
        """

        logger.info(f"Listing scannable objects in {self.cluster}")
        logger.debug(f"Namespaces: {self._config.namespaces}")
        logger.debug(f"Resource Kinds: {self._config.resource_kinds}")
        logger.debug(f"Resource Names: {self._config.resource_names}")

        args = {
            "cloud_account_id": self.cluster,
        }
        # Only include workloads that have at least one active pod in the database
        # This filters out stale workloads that were deleted from K8s but not marked inactive
        query = """SELECT w.namespace, w.name, w.kind, w.labels::text, w.meta::text
             FROM k8s_workloads w
             WHERE w.cloud_account_id = :cloud_account_id
               and w.is_active = true
               and w.namespace != 'kube-system'
               and w.meta->'config'->'containers' is not null
               and jsonb_array_length(w.meta->'config'->'containers') > 0
               and EXISTS (
                   SELECT 1 FROM k8s_pods p
                   WHERE p.cloud_account_id = w.cloud_account_id
                     AND p.namespace = w.namespace
                     AND p.workload_name = w.name
                     AND p.is_active = true
               )
            """
        if self._config.namespaces is not None and len(self._config.namespaces) != 0:
            query += " and w.namespace in :namespaces"
            args["namespaces"] = tuple(self._config.namespaces)
        if self._config.resource_kinds is not None and len(self._config.resource_kinds) != 0:
            query += " and w.kind in :resource_kinds"
            args["resource_kinds"] = tuple(self._config.resource_kinds)
        if self._config.resource_names is not None and len(self._config.resource_names) != 0:
            query += " and w.name in :resource_names"
            args["resource_names"] = tuple(self._config.resource_names)

        # Reduce log verbosity for database queries
        # logger.debug(f"Database query: {query}, args: {args}")

        object_list = []
        with self.engine.connect() as conn:
            result_data = conn.execute(text(query), args)
            for row in result_data.fetchall():
                d = row._asdict()
                namespace = d["namespace"]
                name = d["name"]
                kind = d["kind"]
                meta = d["meta"]
                meta_dict = json.loads(meta)
                if meta_dict.get("config") is not None and meta_dict["config"].get("containers") is not None:
                    for container_info in meta_dict["config"]["containers"]:
                        requests = {
                            ResourceType.CPU: None,
                            ResourceType.Memory: None,
                        }
                        limits = {
                            ResourceType.CPU: None,
                            ResourceType.Memory: None,
                        }

                        # Handle containers with resource requests
                        if (
                            container_info.get("resources") is not None
                            and container_info["resources"].get("requests") is not None
                        ):
                            container_requests = container_info["resources"]["requests"]
                            if container_requests.get("cpu") is not None:
                                requests[ResourceType.CPU] = container_requests["cpu"]
                            if container_requests.get("memory") is not None:
                                requests[ResourceType.Memory] = container_requests["memory"]

                        # Handle containers with resource limits
                        if (
                            container_info.get("resources") is not None
                            and container_info["resources"].get("limits") is not None
                        ):
                            container_limits = container_info["resources"]["limits"]
                            if container_limits.get("cpu") is not None:
                                limits[ResourceType.CPU] = container_limits["cpu"]
                            if container_limits.get("memory") is not None:
                                limits[ResourceType.Memory] = container_limits["memory"]

                        object_list.append(
                            K8sObjectData(
                                cluster=self.cluster,
                                namespace=namespace,
                                name=name,
                                kind=kind,
                                container=container_info["name"],
                                allocations=ResourceAllocations(requests=requests, limits=limits),
                                hpa=None,
                                selector=None,
                            )
                        )
        return object_list

    async def list_pods(self, object: K8sObjectData) -> list[PodData]:
        pod_list = []

        args = {
            "cloud_account_id": self.cluster,
            "namespace": object.namespace,
            "name": object.name,
            "kind": object.kind,
        }
        query = """select name
        from k8s_pods
        where cloud_account_id = :cloud_account_id and is_active = true
            and namespace = :namespace
            and workload_name = :name
            and workload_type = :kind
        order by creation_time desc
        """

        with self.engine.connect() as conn:
            result_data = conn.execute(text(query), args)
            for row in result_data.fetchall():
                d = row._asdict()
                pod_list.append(PodData(name=d["name"], deleted=False))

        return pod_list
