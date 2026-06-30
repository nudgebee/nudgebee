import base64
import hmac
import json
import logging
from datetime import datetime

from flask import request
from werkzeug.exceptions import HTTPException

from config import Configs
from db import clickhouse
from exception.collector_exceptions import BadRequestError, InternalServerError, UnauthorizedError
from controllers.base import BaseController, CredCache
from middleware.utils import validate_key

INVALID_SECRET = "Invalid secret"

# Internal server-to-server auth. The api-server OpenCost-spend-sync cron drives
# ingestion on behalf of every K8s account, so it cannot present a per-agent
# Basic credential. Instead it sends the shared ACTION_API_SERVER_TOKEN (the same
# token the collector uses to call api-server) plus the target account id, and we
# resolve tenant/agent from the agent row. Header names are an internal contract
# between api-server and this collector.
INTERNAL_TOKEN_HEADER = "X-ACTION-TOKEN"
INTERNAL_ACCOUNT_HEADER = "X-NB-Account-Id"

cred_cache = CredCache()


class AuditLogger(BaseController):
    def __init__(self, func):
        super().__init__()
        self.func = func

    def __call__(self, *args, **kwargs):
        try:
            request_start_time = datetime.utcnow()
            response = None
            status_code = 200
            try:
                response = self.func(*args, **kwargs)
            except HTTPException as e:
                status_code = e.code
                raise e
            except Exception as e:
                status_code = 500
                raise e
            finally:
                # Log the incoming request to the audit table
                sensitive_headers = {"authorization", "cookie", "x-api-key", "proxy-authorization"}
                log_entry = {
                    "method": request.method,
                    "url": request.path,
                    "headers": json.dumps(
                        {k: v for k, v in request.headers.items() if k.lower() not in sensitive_headers}
                    ),
                    "client_ip": request.remote_addr,
                    "agent_id": request.agent_id if hasattr(request, "agent_id") else "",
                    "cloud_account_id": request.cloud_account_id if hasattr(request, "cloud_account_id") else "",
                }
                # Record the end time of the request
                request_end_time = datetime.utcnow()
                request_time = int((request_end_time - request_start_time).total_seconds())
                # Update the log entry with response information and request time
                log_entry["time_taken"] = request_time
                if response and len(response) > 0:
                    status_code = response[1]
                log_entry["status_code"] = status_code
                clickhouse.insert_data("agent_audit_log", [log_entry])
            return response
        except Exception as e:
            logging.exception("Failed to handle")
            raise e


class AuthTokenMiddleware(BaseController):
    def __init__(self, func):
        super().__init__()
        self.func = func

    def get_secret_from_db(self, key):
        cursor = self.postgres_client.cursor()
        # Use parameterized query to prevent SQL injection (key comes from user input)
        cursor.execute(
            "select cloud_account_id,tenant,id,access_secret_v2 from agent where type = 'k8s' " "and access_key = %s",
            (key,),
        )
        resp = cursor.fetchone()
        if resp:
            return {
                "id": resp[0],
                "tenant": resp[1],
                "agent_id": resp[2],
                "access_secret_v2": resp[3],
            }
        else:
            raise UnauthorizedError("Invalid key")

    def get_agent_by_account_id(self, account_id):
        with self.postgres_client.cursor() as cursor:
            cursor.execute(
                "select cloud_account_id,tenant,id from agent where type = 'k8s' and cloud_account_id = %s",
                (account_id,),
            )
            resp = cursor.fetchone()
        if resp:
            return {"id": resp[0], "tenant": resp[1], "agent_id": resp[2]}
        raise UnauthorizedError("Invalid account id")

    def authenticate_internal(self) -> bool:
        """Server-to-server auth for the api-server OpenCost-spend-sync cron.

        Returns True when the request carried a valid internal token (and request
        scope has been populated); False when no internal token was present so the
        caller falls back to per-agent Basic auth. A present-but-invalid token
        raises rather than falling through, so a bad internal call can't be retried
        as Basic auth.
        """
        token = request.headers.get(INTERNAL_TOKEN_HEADER)
        if not token:
            return False
        # Fail closed if the shared token is unconfigured — otherwise an empty
        # header would match an empty config and bypass auth.
        if not Configs.ACTION_API_SERVER_TOKEN or not hmac.compare_digest(token, Configs.ACTION_API_SERVER_TOKEN):
            raise UnauthorizedError(INVALID_SECRET)
        account_id = request.headers.get(INTERNAL_ACCOUNT_HEADER)
        if not account_id:
            raise BadRequestError(f"{INTERNAL_ACCOUNT_HEADER} header missing")
        agent = self.get_agent_by_account_id(account_id)
        request.cloud_account_id = agent["id"]
        request.tenant = agent["tenant"]
        request.agent_id = agent["agent_id"]
        return True

    def __call__(self, *args, **kwargs):
        if self.authenticate_internal():
            return self.func(*args, **kwargs)

        if not request.headers.get("Authorization"):
            raise BadRequestError("Authorization header missing")

        api_secret = request.headers.get("Authorization")
        api_secret = api_secret.lstrip("Basic ").strip()
        try:
            # Decode the base64-encoded credentials
            decoded_credentials = base64.b64decode(api_secret).decode("utf-8")
            # Split the decoded credentials into key and secret
            key, api_secret = decoded_credentials.split(":")

            if key is None or api_secret is None:
                raise BadRequestError("Invalid cred format provided")

            if cred_cache.check_key(key):
                value = cred_cache.get_value(key)
            else:
                value = self.get_secret_from_db(key)
                cred_cache.save_value(key=key, value=value)
            if not value:
                raise UnauthorizedError(INVALID_SECRET)
            # v2 bcrypt is the only supported path — legacy v1 AES was
            # removed in B3 after DB confirmed no active agents on it.
            access_secret_v2 = value.get("access_secret_v2") or ""
            if not access_secret_v2:
                raise UnauthorizedError(INVALID_SECRET)
            if not validate_key(api_secret, access_secret_v2):
                raise UnauthorizedError(INVALID_SECRET)

            # add global attributes which can be accessed in the requests
            request.cloud_account_id = value["id"]
            request.tenant = value["tenant"]
            request.agent_id = value["agent_id"]
        except Exception:
            raise UnauthorizedError(INVALID_SECRET)
        return self.func(*args, **kwargs)


class ErrorCatcher(BaseController):
    def __init__(self, func):
        super().__init__()
        self.func = func

    def __call__(self, *args, **kwargs):
        try:
            func_return = self.func(*args, **kwargs)
        except HTTPException as exc:
            logging.warning(exc)
            raise exc
        except Exception as e:
            logging.exception(e)
            raise InternalServerError("Something went wrong")
        return func_return
