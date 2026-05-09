import json
import logging
import os
import requests
from typing import Dict, Any

LOG = logging.getLogger(__name__)


def execute(operation: str, variables: Dict[str, Any]) -> Dict[str, Any]:
    try:
        headers = {
            "Content-Type": "application/json",
            "x-hasura-admin-secret": os.environ.get("HASURA_GRAPHQL_ADMIN_SECRET", "admin_secret"),
        }

        endpoint = os.environ.get("HASURA_GRAPHQL_ENDPOINT", "http://localhost:8080") + "/v1/graphql"
        response = requests.post(endpoint, headers=headers, json={"query": operation, "variables": variables})
        data = response.json()
        return {"data": data.get("data"), "errors": data.get("errors")}
    except Exception as err:
        LOG.exception(err)
        return {"data": None, "errors": [str(err)]}
