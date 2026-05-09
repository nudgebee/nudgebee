import logging

from notifications_server.utils.gql import execute

LOG = logging.getLogger(__name__)

HASURA_UPDATE_SLACK_STATE_TENANT = (
    "mutation UpdateTenantForSlackState($state: String!, $tenant_id: uuid!) {"
    "update_slack_oauth_states(where: {state: {_eq: $state}}, _set: {tenant_id: $tenant_id}) { returning { state } } }"
)

HASURA_FETCH_SLACK_STATE = (
    "query GetTenantFromState($object: String!) {slack_oauth_states(where: {state: {_eq: $object}}) { tenant_id } }"
)

HASURA_FETCH_RESOURCE = (
    "query FetchResourceDetails($object: uuid!) {cloud_resourses(where: {id: {_eq: $object}}) { "
    "name service_name type arn cloud_account { account_name account_number cloud_provider } } }"
)

HASURA_LIST_TICKET_CONFIGURATIONS = (
    "query TicketConfigurations($object: uuid!) { jira_configurations(where: {tenant: {_eq: $object} }) { id name } }"
)

HASURA_FETCH_TICKET_CONFIGURATION = (
    "query TicketConfigurations($object: uuid!) { jira_configurations(where: {id: {_eq: $object} }) { id name "
    "projects priorities } }"
)

HASURA_LIST_USERS = (
    "query ListTenantUsers($object: uuid!) { tenant_users(where: {tenant: {_eq: $object}}) { id "
    "userByUser { username } } }"
)

HASURA_FETCH_FINDING = (
    "query FetchFindingEvent($object: uuid!) { events(where: {id: {_eq: $object}}) { id title cluster "
    "subject_namespace description evidences } }"
)

HASURA_FETCH_ACCOUNT = (
    "query FetchAccountDetails($object: String!) { cloud_accounts(where: {account_name: {_ilike: $object}}) { "
    "id account_name tenant } }"
)

HASURA_FETCH_ACCOUNT_BY_ID = (
    "query FetchAccountById($object: uuid!) { cloud_accounts(where: {id: {_eq: $object}}) { id account_name tenant } }"
)

HASURA_ACCOUNT_LIST = (
    "query ListAccounts($object: [uuid!]) { cloud_accounts(where:{tenant:{_in: $object}, "
    'status: {_eq: active}, agents: {status: {_eq: "CONNECTED"}}}) { id account_name } }'
)

HASURA_FETCH_USER_BY_EMAIL = (
    "query GetUserByEmail($object: citext!) { users(where: {username: {_eq: $object}}) { username id display_name } }"
)

HASURA_FETCH_USER_TENANTS = (
    "query Tenants($object: citext!) { tenant_users(where: {userByUser: {username: {_eq: $object}}}) { user tenant } }"
)

HASURA_LIST_INSIGHTS = (
    'query GetK8sInsights($account_id: uuid!) { insight(where: {status: {_neq: "CLOSED"}, account_id: {_eq:'
    " $account_id}}) { title status source }}"
)

HASURA_LIST_K8S_PODS = (
    "query k8s_pods_list { k8s_pods(where: "
    '{_and: [{cloud_account_id: {_eq: "$account_id"}}, {namespace: {_eq: "$namespace"}}, {is_active: {_eq: true}}]}, '
    "order_by: {creation_time: desc_nulls_last}) { id: cloud_resource_id namespace name workload_name } }"
)

HASURA_FETCH_POD_DETAILS = (
    'query getPodDetails { cloud_resourses(where: {name: {_ilike:"$pod_name"}}) {'
    "id meta is_active account name service_name tenant } } "
)

HASURA_CREATE_AI_FEEDBACK = (
    "mutation CreateAiFeedback($object: AiFeedbackCreateRequest!) { ai_feedback_create(request: $object) "
    "{ data { success } } }"
)

HASURA_GET_LLM_CONVERSATION = (
    "query LLMConversationHistory($object: uuid!) { "
    "llm_conversations(where: {id: {_eq: $object}}, order_by: {updated_at: desc}, limit: 10, offset: 0) "
    "{ id updated_at status user_id session_id source "
    'for_message: llm_conversation_messages(limit: 10, where: {message_type: {_neq: "router"}}, '
    "order_by: {updated_at: desc}) { id message response message_type message_config parent_agent_id created_at } } }"
)

HASURA_GET_LLM_CONVERSATION_BY_SESSION = (
    "query GetLLMConversationBySession($object: String!) {llm_conversations(where: {session_id: {_eq: $object}}, "
    "limit: 1) { id user_id session_id account_id tenant_id } }"
)


def update_slack_state_tenant(state, tenant_id):
    response = execute(HASURA_UPDATE_SLACK_STATE_TENANT, {"state": state, "tenant_id": tenant_id})
    if response.get("errors") is not None:
        raise ValueError("Unable to add tenant to slack OAuth state")


def update_slack_installation_tenant(state):
    response = execute(HASURA_FETCH_SLACK_STATE, {"state": state})
    if response.get("errors") is not None:
        raise ValueError("Unable to get tenant for slack OAuth state")
    return response.get("data").get("tenant_id")


def get_resource_details(cloud_resource_id):
    if not cloud_resource_id:
        return None
    resource_json = execute(HASURA_FETCH_RESOURCE, {"object": cloud_resource_id})
    if resource_json.get("errors"):
        LOG.exception("Unable to fetch resource details for cloud resource id: ", cloud_resource_id)
        return None
    if len(resource_json.get("data").get("cloud_resourses")) == 0:
        return None
    return resource_json.get("data").get("cloud_resourses")[0]


def execute_and_get_result(query, _object):
    return execute(query, {"object": _object})


def execute_and_get_result_v2(query, variables):
    return execute(query, variables)
