import time

from pydantic import BaseModel

from notifications_server.message_templates.blocks import CallbackChoice
from notifications_server.utils.action_requests import ActionRequestBody, ExternalActionRequest, sign_action_request


class ExternalActionRequestBuilder(BaseModel):
    @classmethod
    def create_for_func(
        cls,
        choice: CallbackChoice,
        text: str,
        signing_key: str,
    ):
        if choice.action is None:
            raise ValueError(
                f"The callback for choice {text} is None. Did you accidentally pass `foo()` as a callback and not"
                " `foo`?"
            )

        action_params = {} if choice.action_params is None else choice.action_params.model_dump()

        body = ActionRequestBody(
            timestamp=int(time.time()),
            action_name=action_params["action_name"],
            action_params=action_params,
            origin="callback",
        )
        return ExternalActionRequest(
            body=body,
            signature=sign_action_request(body, signing_key),
        )
