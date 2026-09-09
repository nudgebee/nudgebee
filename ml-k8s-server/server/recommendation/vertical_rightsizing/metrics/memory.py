from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData

from .base import PrometheusMetric, QueryType


class MemoryLoader(PrometheusMetric):
    """
    A metric loader for loading memory usage metrics.
    """

    query_type: QueryType = QueryType.QueryRange

    def get_query(self, object: K8sObjectData, duration: str, step: str) -> str:
        pods_selector = "|".join(pod.name for pod in object.pods)
        if pods_selector == "":
            pods_selector = ".*"
        cluster_label = self.get_prometheus_cluster_label()
        return f"""
            max(
                container_memory_working_set_bytes{{
                    namespace="{object.namespace}",
                    pod=~"{pods_selector}",
                    container="{object.container}",
                    {cluster_label}
                }}
            ) by (container, pod, job)
        """


class MaxMemoryLoader(PrometheusMetric):
    """
    A metric loader for loading max memory usage metrics.
    """

    def get_query(self, object: K8sObjectData, duration: str, step: str) -> str:
        pods_selector = "|".join(pod.name for pod in object.pods)
        if pods_selector == "":
            pods_selector = ".*"
        cluster_label = self.get_prometheus_cluster_label()
        return f"""
            max_over_time(
                max(
                    container_memory_working_set_bytes{{
                        namespace="{object.namespace}",
                        pod=~"{pods_selector}",
                        container="{object.container}",
                        {cluster_label}
                    }}
                ) by (container, pod, job)
                [{duration}:{step}]
            )
        """


class MaxUsageMemoryLoader(PrometheusMetric):
    """
    A metric loader for the cgroup's own memory high-water mark.

    `container_memory_working_set_bytes` is a gauge sampled at the scrape interval (~40s),
    so `MaxMemoryLoader` misses any peak that rises and falls between two scrapes.
    `container_memory_max_usage_bytes` is the high-water mark the kernel itself maintains,
    so it cannot miss a peak. It is used only as a floor for the memory *limit* - it counts
    reclaimable page cache, so it is not a safe basis for the request.

    Not exported on every cAdvisor/cgroup version; missing data degrades to the old behaviour.
    """

    warning_on_no_data = False

    def get_query(self, object: K8sObjectData, duration: str, step: str) -> str:
        # Deliberately no ".*" fallback, unlike the loaders above. A wildcard would match every
        # pod in the namespace sharing this container name, and a limit floor taken from an
        # unrelated workload's memory is worse than no floor at all. Uppercase and underscores
        # are illegal in a pod name, so this selector matches nothing, the loader returns no
        # data, and the limit falls back to the request - the behaviour before it existed.
        pods_selector = "|".join(pod.name for pod in object.pods)
        if pods_selector == "":
            pods_selector = "__NO_PODS_RESOLVED__"
        cluster_label = self.get_prometheus_cluster_label()
        return f"""
            max_over_time(
                max(
                    container_memory_max_usage_bytes{{
                        namespace="{object.namespace}",
                        pod=~"{pods_selector}",
                        container="{object.container}",
                        {cluster_label}
                    }}
                ) by (container, pod, job)
                [{duration}:{step}]
            )
        """


class MemoryAmountLoader(PrometheusMetric):
    """
    A metric loader for loading memory points count.
    """

    def get_query(self, object: K8sObjectData, duration: str, step: str) -> str:
        pods_selector = "|".join(pod.name for pod in object.pods)
        if pods_selector == "":
            pods_selector = ".*"
        cluster_label = self.get_prometheus_cluster_label()
        return f"""
            count_over_time(
                max(
                    container_memory_working_set_bytes{{
                        namespace="{object.namespace}",
                        pod=~"{pods_selector}",
                        container="{object.container}",
                        {cluster_label}
                    }}
                ) by (container, pod, job)
                [{duration}:{step}]
            )
        """


# TODO: Need to battle test if this one is correct.
class MaxOOMKilledMemoryLoader(PrometheusMetric):
    """
    A metric loader for loading the maximum memory limits that were surpassed by the OOMKilled event.
    """

    warning_on_no_data = False

    def get_query(self, object: K8sObjectData, duration: str, step: str) -> str:
        pods_selector = "|".join(pod.name for pod in object.pods)
        if pods_selector == "":
            pods_selector = ".*"
        cluster_label = self.get_prometheus_cluster_label()
        return f"""
            max_over_time(
                max(
                    max(
                        kube_pod_container_resource_limits{{
                            resource="memory",
                            namespace="{object.namespace}",
                            pod=~"{pods_selector}",
                            container="{object.container}",
                            {cluster_label}
                        }}
                    ) by (pod, container, job)
                    * on(pod, container, job) group_left(reason)
                    max(
                        kube_pod_container_status_last_terminated_reason{{
                            reason="OOMKilled",
                            namespace="{object.namespace}",
                            pod=~"{pods_selector}",
                            container="{object.container}",
                            {cluster_label}
                        }}
                    ) by (pod, container, job, reason)
                ) by (container, pod, job)
                [{duration}:{step}]
            )
        """
