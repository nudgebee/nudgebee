import hashlib
import hmac
import logging
from typing import Annotated, List, Optional
from uuid import UUID

import requests
from pydantic import BaseModel, BeforeValidator

from notifications_server.configs.settings import settings
from notifications_server.utils.documented_pydantic import DocumentedModel


class ActionParams(DocumentedModel):
    """
    Base class for all Action parameter classes.
    """

    def post_initialization(self):
        """
        This function can be used to run post initialization logic on the action params
        """
        pass


class ActionRequestBody(BaseModel):
    action_name: str
    timestamp: Annotated[int, BeforeValidator(lambda v: int(v))]
    action_params: Optional[dict] = None
    sinks: Optional[List[str]] = None
    origin: Optional[str] = None


class ExternalActionRequest(BaseModel):
    body: ActionRequestBody
    signature: Optional[str] = ""  # Used for signature based auth protocol option
    partial_auth_a: str = ""  # Auth for public key auth protocol option - should be added by the client
    partial_auth_b: str = ""  # Auth for public key auth protocol option - should be added by the relay
    request_id: str = ""  # If specified, should return a sync response using the specified request_id
    no_sinks: bool = False  # Indicates not to send to sinks at all. The request body has a sink list,
    # however an empty sink list means using the server default sinks


class PartialAuth(BaseModel):
    hash: str
    key: UUID


def sign_action_request(body: BaseModel, signing_key: str):
    import json

    format_req = str.encode(f"v0:{json.dumps(body.model_dump(exclude_none=True), sort_keys=True)}")
    if not signing_key:
        raise ValueError("Signing key not available. Cannot sign action request")
    request_hash = hmac.new(signing_key.encode(), format_req, hashlib.sha256).hexdigest()
    return f"v0={request_hash}"


class OutgoingActionRequest:
    @staticmethod
    def send(body: ActionRequestBody, signing_key: str):
        action_request = ExternalActionRequest(
            signature=sign_action_request(body, signing_key),
            body=body,
        )

        resp = requests.post(f"{settings.urls.base_url}/api/trigger", json=action_request.model_dump())
        if resp.status_code != 200:
            logging.error(f"Failed to send external action request {resp.status_code} {resp.reason}")
