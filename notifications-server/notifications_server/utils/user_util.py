from notifications_server.utils.gql import execute

HASURA_GET_USER_ID = "query GetUserIdByEmail($email: citext) { users(where: { username: { _eq: $email } }) {id}}"


def get_user_id_by_email(email):
    response_json = execute(HASURA_GET_USER_ID, {"email": email})
    if response_json.get("errors"):
        return None

    if len(response_json.get("data").get("users")) > 0:
        return response_json.get("data").get("users")[0].get("id")
    return None
