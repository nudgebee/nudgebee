import datetime
import logging
import warnings
from enum import Enum
from typing import Union

import pulp
from pandas.errors import SettingWithCopyWarning
from sqlalchemy import text
from sqlalchemy.orm import sessionmaker

from server.controllers.models.cluster_rightsizing_model import (
    InstanceTypeRecommendation,
    ClusterRightsizingRequest,
    ClusterRightSizingResponse,
)
from server.metrics.prometheus_metrics import Prometheus
from server.utils.utils import DatabaseEngine

logger = logging.getLogger(__name__)
warnings.simplefilter(action="ignore", category=SettingWithCopyWarning)

k8s_memory_factors = {
    "m": 1 / 1000,  # milli
    "u": 1 / (1000 * 1000),  # micro
    "n": 1 / (1000 * 1000 * 1000),  # nano
    "K": 1000,
    "M": 1000 * 1000,
    "G": 1000 * 1000 * 1000,
    "T": 1000 * 1000 * 1000 * 1000,
    "P": 1000 * 1000 * 1000 * 1000 * 1000,
    "E": 1000 * 1000 * 1000 * 1000 * 1000 * 1000,
    "k": 1024,
    "Ki": 1024,
    "Mi": 1024 * 1024,
    "Gi": 1024 * 1024 * 1024,
    "Ti": 1024 * 1024 * 1024 * 1024,
    "Pi": 1024 * 1024 * 1024 * 1024 * 1024,
    "Ei": 1024 * 1024 * 1024 * 1024 * 1024 * 1024,
}


class ClusterProvider(Enum):
    AWS = "eks"
    GCP = "gke"
    AZURE = "aks"

    @classmethod
    def get_provider(cls, provider: str) -> Union[str, None]:
        if provider.lower() == ClusterProvider.AWS.value:
            return ClusterProvider.AWS.name.lower()
        elif provider.lower() == ClusterProvider.GCP.value:
            return ClusterProvider.GCP.name.lower()
        elif provider.lower() == ClusterProvider.AZURE.value:
            return ClusterProvider.AZURE.name.lower()
        else:
            return provider.lower()


class InstanceMeta:
    def __init__(self, cloud_provider, region, instance_type, cpu, memory, cost, network_performance, attributes=None):
        self.cloud_provider = cloud_provider
        self.region = region
        self.instance_type = instance_type
        self.cpu = cpu
        self.memory = memory
        self.cost = cost
        self.network_performance = network_performance
        self.attributes = attributes if attributes else {}


class ClusterRightSizingRecommendation:
    def __init__(
        self,
        url: str,
        account_id: str,
        tenant_id: str,
        persist_recommendation: bool,
    ):
        self.account_id = account_id
        self.tenant_id = tenant_id
        self.persist_recommendation = persist_recommendation
        try:
            self.engine = DatabaseEngine.get_engine()
        except Exception as e:
            msg = f"Failed to create engine : {e}"
            logger.error(msg)
            raise ValueError(msg)

    def _calculate_effective_resources(self, instances, reserve_percent=0.1, daemonset_cpu=0.1, daemonset_memory=0.1):
        for instance in instances:
            instance.effective_memory = instance.memory * (1 - reserve_percent) - daemonset_memory
            instance.effective_cpu = instance.cpu * (1 - reserve_percent) - daemonset_cpu

    def _calculate_recommendations(
        self,
        instances,
        max_memory_request,
        max_cpu_request,
        min_nodes,
        num_combinations,
        daemonset_memory,
        daemonset_cpu,
        preferred_instance_groups=None,
        graviton_instances=False,
        min_cpu_per_node=2,
        min_memory_per_node=4,
    ):
        recommendations = []
        used_combinations = []

        if not graviton_instances:
            # filter graviton instances and handle argument of type 'NoneType' is not iterable
            instances = [
                instance
                for instance in instances
                if not instance.attributes.get("physicalProcessor")
                or "Graviton" not in instance.attributes.get("physicalProcessor")
            ]

        if preferred_instance_groups:
            # preferred instance group like ["m", "c"]
            instances = [
                instance
                for instance in instances
                if instance.instance_type.startswith(tuple(preferred_instance_groups))
            ]
        # filter out instances that do not meet the minimum memory and cpu requirements
        instances = [
            instance
            for instance in instances
            if instance.memory >= min_memory_per_node and instance.cpu >= min_cpu_per_node
        ]

        self._calculate_effective_resources(instances, daemonset_cpu=daemonset_cpu, daemonset_memory=daemonset_memory)
        for _ in range(num_combinations):
            # Create a linear programming problem
            prob = pulp.LpProblem("Instance_Type_Optimization", pulp.LpMinimize)

            # Create decision variables for each instance type
            instance_vars = pulp.LpVariable.dicts("Instances", range(len(instances)), 0, None, pulp.LpInteger)

            # Objective function: Minimize the total cost
            prob += pulp.lpSum([instance_vars[i] * instances[i].cost for i in range(len(instances))])

            # Constraints
            prob += (
                pulp.lpSum([instance_vars[i] * instances[i].effective_memory for i in range(len(instances))])
                >= max_memory_request
            )
            prob += (
                pulp.lpSum([instance_vars[i] * instances[i].effective_cpu for i in range(len(instances))])
                >= max_cpu_request
            )
            prob += pulp.lpSum([instance_vars[i] for i in range(len(instances))]) >= min_nodes

            # Add constraints to exclude previously found combinations
            for comb in used_combinations:
                prob += pulp.lpSum([instance_vars[i] for i in comb]) <= len(comb) - 1

            # Solve the problem
            if pulp.PULP_CBC_CMD().available():
                solver = pulp.PULP_CBC_CMD(msg=False)
            elif pulp.GLPK_CMD().available():
                solver = pulp.GLPK_CMD(msg=False)
            elif pulp.COIN_CMD().available():
                solver = pulp.COIN_CMD(msg=False)
            else:
                solver = None
            prob.solve(solver=solver)

            # Collect the results
            instance_types = []
            total_memory = 0
            total_cpu = 0
            total_cost = 0
            number_of_nodes = 0
            current_combination = []
            network_profiles = []

            for i in range(len(instances)):
                if instance_vars[i].varValue > 0:
                    instance_types.extend([instances[i].instance_type] * int(instance_vars[i].varValue))
                    total_memory += (instances[i].effective_memory + daemonset_memory) * int(instance_vars[i].varValue)
                    total_cpu += (instances[i].effective_cpu + daemonset_cpu) * int(instance_vars[i].varValue)
                    total_cost += instances[i].cost * int(instance_vars[i].varValue)
                    number_of_nodes += int(instance_vars[i].varValue)
                    current_combination.append(i)
                    network_profiles.append(instances[i].network_performance)

            if current_combination:
                recommendation = InstanceTypeRecommendation(
                    instance_types,
                    instances[0].region,
                    total_memory,
                    total_cpu,
                    total_cost,
                    number_of_nodes,
                    network_profiles,
                    daemonset_memory,
                    daemonset_cpu,
                    graviton_instances,
                )
                recommendations.append(recommendation)
                used_combinations.append(current_combination)

        return recommendations

    def classify_network_performance(self, network_performance):
        if network_performance is None:
            return "unknown"
        if "25 Gigabit" in network_performance or "25 Gbps" in network_performance:
            return "high"
        elif "10 Gigabit" in network_performance or "10 Gbps" in network_performance:
            return "medium"
        else:
            return "low"

    def load_pricing_data(self, provider: str, region: str):
        instances = []
        query = text(
            """SELECT resource_type, resource_region, resource_cost, resource_capacity ->> 'memory_gb' AS memory,
             resource_capacity ->> 'cpu_virtual' AS cpu, resource_cost, attributes FROM cloud_resource_details
             WHERE service_type = 'Compute' AND cloud_provider = :provider AND resource_region = :region
             AND resource_cost > 0"""
        )
        with self.engine.connect() as conn:
            result_data = conn.execute(query, {"provider": provider, "region": region})
            for row in result_data.fetchall():
                d = row._asdict()
                instance_type = d["resource_type"]
                location = d["resource_region"]
                cpu = float(d["cpu"])
                memory = float(d["memory"])
                cost = float(d["resource_cost"])
                network_performance = None
                if "networkPerformance" in d["attributes"]:
                    network_performance = self.classify_network_performance(d["attributes"]["networkPerformance"])

                instances.append(
                    InstanceMeta(
                        provider, location, instance_type, cpu, memory, cost, network_performance, d["attributes"]
                    )
                )
        return instances

    @staticmethod
    def parse_cpu(cpu: str) -> float:
        if not cpu:
            return 0.0
        if "m" in cpu:
            return round(float(cpu.replace("m", "").strip()) / 1000, 3)
        if "k" in cpu:
            return round(float(cpu.replace("k", "").strip()) * 1000, 3)
        return round(float(cpu), 3)

    @staticmethod
    def parse_mem(mem: str) -> float:
        if not mem:
            return 0
        num_of_bytes = ClusterRightSizingRecommendation.get_number_of_bytes_from_kubernetes_mem_spec(mem)
        return float(num_of_bytes / (1024 * 1024 * 1024))

    @staticmethod
    def get_number_of_bytes_from_kubernetes_mem_spec(mem_spec: str) -> int:
        try:
            if not mem_spec:
                return 0

            if len(mem_spec) > 2 and mem_spec[-2:] in k8s_memory_factors:
                return int(int(mem_spec[:-2]) * k8s_memory_factors[mem_spec[-2:]])

            if len(mem_spec) > 1 and mem_spec[-1] in k8s_memory_factors:
                return int(int(mem_spec[:-1]) * k8s_memory_factors[mem_spec[-1]])

            if mem_spec.isdigit():
                return int(mem_spec)

            return int(float(mem_spec))

        except Exception:  # could be a valueError with mem_spec
            logging.error(f"error parsing memory {mem_spec}", exc_info=True)
        return 0

    def get_current_pod_allocation(self):
        query = text("""SELECT meta->'config' -> 'containers' AS containers,
                      workload_name,
                      workload_type,
                      "namespace"
               FROM k8s_pods ksp
               WHERE workload_type != 'DaemonSet'
               AND is_active IS NOT FALSE
               AND cloud_account_id = :account_id
               GROUP BY workload_name, workload_type, "namespace", meta->'config' -> 'containers'""")
        with self.engine.connect() as conn:
            data = conn.execute(query, {"account_id": self.account_id})
            total_cpu_request = 0
            total_memory_request = 0
            result_data = data.fetchall()
            for row in result_data:
                pod = row._asdict()
                for container in pod.get("containers"):
                    if "requests" in container["resources"]:
                        total_cpu_request += self.parse_cpu(container["resources"].get("requests").get("cpu"))
                        total_memory_request += self.parse_mem(container.get("resources").get("requests").get("memory"))

        return total_cpu_request, total_memory_request

    def get_historical_pod_allocation(self):
        prometheus = Prometheus(account_id=self.account_id, namespace_name="", deployment_name="")
        start_time = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=1)
        cpu_requests: list[list[float]] = prometheus.query_metrics(
            query='sum(kube_pod_container_resource_requests{  __CLUSTER__  resource="cpu"} * on (pod, namespace)'
            ' group_left kube_pod_owner{  __CLUSTER__  owner_kind!="DaemonSet"})',
            start_time=start_time,
            end_time=datetime.datetime.now(datetime.timezone.utc),
        )
        max_cpu_requests = 0
        for request in cpu_requests:
            if float(request[1]) > max_cpu_requests:
                max_cpu_requests = float(request[1])

        memory_requests = prometheus.query_metrics(
            query='sum(kube_pod_container_resource_requests{  __CLUSTER__  resource="memory"} * on (pod, namespace)'
            'group_left kube_pod_owner{  __CLUSTER__  owner_kind!="DaemonSet"})',
            start_time=start_time,
            end_time=datetime.datetime.now(datetime.timezone.utc),
        )

        max_memory_requests = 0
        for request in memory_requests:
            if float(request[1]) > max_memory_requests:
                max_memory_requests = float(request[1])

        return max_cpu_requests, max_memory_requests / (1024 * 1024 * 1024)

    def get_current_node_allocation(self):
        query = text(
            "SELECT node_flavor, node_type, node_region, meta->>'memory_capacity' AS memory_capacity, "
            "meta->>'cpu_capacity' AS cpu_capacity "
            "FROM k8s_nodes ksn "
            "WHERE cloud_account_id = :account_id AND is_active IS NOT FALSE"
        )
        session = sessionmaker(bind=self.engine)
        session = session()
        try:
            data = session.execute(query, {"account_id": self.account_id})
            instances = [dict(row._mapping) for row in data.fetchall()]
            return instances
        finally:
            session.commit()
            session.close()

    def get_demonset_allocations(self):
        query = text("""SELECT meta->'config' -> 'containers' AS containers,
                      workload_name,
                      workload_type,
                      "namespace"
               FROM k8s_pods ksp
               WHERE workload_type = 'DaemonSet'
               AND is_active IS NOT FALSE
               AND cloud_account_id = :account_id
               GROUP BY workload_name,
                        workload_type,
                        "namespace",
                        meta->'config' -> 'containers'""")
        with self.engine.connect() as conn:
            data = conn.execute(query, {"account_id": self.account_id})
            total_cpu_request = 0
            total_memory_request = 0
            result_data = data.fetchall()

            for row in result_data:
                pod = row._asdict()
                for container in pod.get("containers"):
                    resources = container.get("resources")
                    if not isinstance(resources, dict):
                        continue
                    requests = resources.get("requests")
                    if not isinstance(requests, dict):
                        continue
                    total_cpu_request += self.parse_cpu(requests.get("cpu"))
                    total_memory_request += self.parse_mem(requests.get("memory"))

        # convert total memory to GB
        return total_cpu_request, total_memory_request

    def get_cloud_provider(self) -> Union[str, None]:
        query = text("""SELECT k8s_provider
               FROM agent
               WHERE cloud_account_id = :account_id""")
        with self.engine.connect() as conn:
            data = conn.execute(query, {"account_id": self.account_id})
            result_data = data.fetchall()
            if len(result_data) == 0 or not result_data[0]:
                raise ValueError("Cloud Provider not found")
            provider = result_data[0][0]
        return ClusterProvider.get_provider(provider)

    def get_cloud_region(self):
        query = text("""SELECT DISTINCT ksn.node_region
               FROM k8s_nodes ksn
               WHERE cloud_account_id = :account_id
               AND is_active IS NOT FALSE""")
        with self.engine.connect() as conn:
            data = conn.execute(query, {"account_id": self.account_id})
            result_data = data.fetchall()
            if len(result_data) == 0 or not result_data[0]:
                raise ValueError("Cloud Region not found")
        return result_data[0][0]

    def generate_recommendation(self, request: ClusterRightsizingRequest) -> ClusterRightSizingResponse:
        provider = self.get_cloud_provider()
        node_region = self.get_cloud_region()
        if provider is None:
            raise ValueError("Cloud Provider not found")
        instances = self.load_pricing_data(provider, node_region)
        try:
            max_cpu_request, max_memory_request = self.get_historical_pod_allocation()
        except Exception as e:
            logger.error(f"Failed to get historical pod allocation, falling back to current allocation: {e}")
            max_cpu_request, max_memory_request = self.get_current_pod_allocation()
        demonset_cpu_request, demonset_memory_request = self.get_demonset_allocations()
        current_node = self.get_current_node_allocation()
        min_nodes = request.min_nodes
        num_combinations = request.number_of_recommendations
        recommendations = []
        if request.graviton:
            recommendations.extend(
                self._calculate_recommendations(
                    instances,
                    max_memory_request,
                    max_cpu_request,
                    min_nodes,
                    num_combinations,
                    demonset_memory_request,
                    demonset_cpu_request,
                    request.preferred_instance_groups,
                    request.graviton,
                    request.min_cpu_per_node,
                    request.min_memory_per_node,
                )
            )

        recommendations.extend(
            self._calculate_recommendations(
                instances,
                max_memory_request,
                max_cpu_request,
                min_nodes,
                num_combinations,
                demonset_memory_request,
                demonset_cpu_request,
                request.preferred_instance_groups,
                False,
                request.min_cpu_per_node,
                request.min_memory_per_node,
            )
        )
        current_instances: list[str] = []
        region: str = ""
        total_memory: float = 0
        total_cpu: float = 0
        cost = 0
        network_profile = []
        for node in current_node:
            current_instances.append(node.get("node_flavor"))
            region = node.get("node_region")
            total_memory += float(node.get("memory_capacity"))
            total_cpu += float(node.get("cpu_capacity"))
            for instance in instances:
                if instance.instance_type == node.get("node_flavor"):
                    cost += instance.cost
                    network_profile.append(self.classify_network_performance(instance.network_performance))
                    break
        current_node_list = InstanceTypeRecommendation(
            instance_types=current_instances,
            region=region,
            total_memory=int(total_memory / 1024),
            total_cpu=int(total_cpu),
            cost=cost,
            number_of_nodes=len(current_instances),
            network_profile=network_profile,
            reserved_memory=demonset_memory_request,
            reserved_cpu=demonset_cpu_request,
            graviton=False,
        )
        return ClusterRightSizingResponse(current_node_list, recommendations)
