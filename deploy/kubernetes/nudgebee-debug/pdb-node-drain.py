from kubernetes import client, config
from datetime import datetime
import logging

# Configure logging
logging.basicConfig(
    format='%(asctime)s - %(levelname)s - %(message)s',
    level=logging.INFO
)
logger = logging.getLogger("kubernetes_audit")

def get_recent_node_events(api_client):
    """
    Fetches recent Node events where DisruptionBlocked is reported.

    Args:
        api_client: Kubernetes API client.

    Returns:
        list: List of unique node names.
    """
    v1 = client.CoreV1Api(api_client)
    try:
        events = v1.list_event_for_all_namespaces(
            field_selector="involvedObject.kind=Node,reason=DisruptionBlocked"
        )
        nodes = []

        for event in events.items:
            last_timestamp = event.last_timestamp
            if last_timestamp:
                now_timestamp = datetime.utcnow().timestamp()
                event_timestamp = last_timestamp.timestamp()

                if now_timestamp - event_timestamp <= 600:  # Check if event occurred in the last 10 minutes
                    node_name = event.involved_object.name
                    if node_name not in nodes:
                        nodes.append(node_name)

        logger.info(f"Identified nodes with recent events: {nodes}")
        return nodes

    except Exception as e:
        logger.error(f"Error fetching node events: {e}")
        return []

def get_pdbs_for_nodes_with_min_available(api_client, nodes):
    """
    Fetches PDBs associated with pods running on the specified nodes,
    filtering only those PDBs with `minAvailable` set to 1.

    Args:
        api_client: Kubernetes API client.
        nodes (list): List of node names.

    Returns:
        dict: Mapping of nodes to their associated PDBs with `minAvailable=1`.
    """
    v1 = client.CoreV1Api(api_client)
    policy_api = client.PolicyV1Api(api_client)

    node_pdb_mapping = {}

    for node in nodes:
        try:
            # Get pods scheduled on the current node
            pods = v1.list_pod_for_all_namespaces(field_selector=f"spec.nodeName={node}")
            pod_labels_list = [pod.metadata.labels or {} for pod in pods.items]

            # Get all PDBs
            pdbs = policy_api.list_pod_disruption_budget_for_all_namespaces()

            pdbs_for_node = []
            for pdb in pdbs.items:
                min_available = pdb.spec.min_available
                if isinstance(min_available, int) and min_available != 1:
                    continue

                pdb_selector = pdb.spec.selector.match_labels or {}

                for pod_labels in pod_labels_list:
                    if all(pod_labels.get(k) == v for k, v in pdb_selector.items()):
                        pdbs_for_node.append(pdb)
                        break

            node_pdb_mapping[node] = pdbs_for_node

            logger.info(f"Node {node} has associated PDBs: {[pdb.metadata.name for pdb in pdbs_for_node]}")

        except Exception as e:
            logger.error(f"Error processing node {node}: {e}")
            node_pdb_mapping[node] = []

    return node_pdb_mapping

def update_pdb_min_available(api_client, pdb_name, namespace, min_available):
    """
    Updates the `minAvailable` field of a PodDisruptionBudget (PDB) resource.

    Args:
        api_client: Kubernetes API client.
        pdb_name (str): The name of the PDB resource.
        namespace (str): The namespace of the PDB resource.
        min_available (int): The new value for `minAvailable`.

    Returns:
        dict: The updated PDB resource.
    """
    policy_api = client.PolicyV1Api(api_client)

    body = {"spec": {"minAvailable": min_available}}
    try:
        updated_pdb = policy_api.patch_namespaced_pod_disruption_budget(pdb_name, namespace, body)
        logger.info(f"Successfully updated PDB {pdb_name} in namespace {namespace} to minAvailable={min_available}")
        return updated_pdb

    except Exception as e:
        logger.error(f"Error updating PDB {pdb_name} in namespace {namespace}: {e}")
        return None

if __name__ == "__main__":
    config.load_kube_config()
    api_client = client.ApiClient()

    try:
        nodes = get_recent_node_events(api_client)
        if not nodes:
            logger.warning("No recent node events found.")

        pdb_mapping = get_pdbs_for_nodes_with_min_available(api_client, nodes)

        for node, pdbs in pdb_mapping.items():
            for pdb in pdbs:
                logger.info(f"Preparing to update PDB {pdb.metadata.name} in namespace {pdb.metadata.namespace} for node {node}")
                updated_pdb = update_pdb_min_available(
                    api_client,
                    pdb_name=pdb.metadata.name,
                    namespace=pdb.metadata.namespace,
                    min_available=0,
                )
                if updated_pdb:
                    logger.info(f"Successfully updated PDB: {updated_pdb.metadata.name}")

    except Exception as e:
        logger.critical(f"Critical error occurred: {e}")
