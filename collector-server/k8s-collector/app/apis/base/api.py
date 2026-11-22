import gzip
import json
import zlib

from flask import request
from flask_restx import Resource

from exception.collector_exceptions import BadRequestError
from middleware.auth_token_middleware import AuthTokenMiddleware, ErrorCatcher, AuditLogger


def read_json_body():
    """Parse the request body as JSON, gunzipping it first when it is gzip-encoded.

    A body that arrives empty, cut off or garbled is the client's problem and
    answers 400. It used to surface as a 500 with a traceback: an agent that
    gives up mid-upload leaves an empty body behind, and the error log blamed
    this service for it.
    """
    if request.headers.get("Content-Encoding") != "gzip":
        return request.get_json()
    try:
        return json.loads(gzip.decompress(request.get_data()).decode("utf-8"))
    except (OSError, EOFError, zlib.error, ValueError) as e:
        raise BadRequestError(f"Unreadable gzip JSON body: {e}") from e


class BaseApi(Resource):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)
        self._controller = None
        self

    @staticmethod
    def build_single_response(data, status_code=200, headers=None) -> tuple:
        body = {"data": data}
        lambda_headers = {
            "Content-Type": "application/json",
        }
        if headers is not None:
            lambda_headers.update(**headers)
        return (body, status_code, lambda_headers)

    @staticmethod
    def build_response(data, status_code=200, headers=None) -> tuple:
        body = {
            "data": data,
        }

        lambda_headers = {
            "Content-Type": "application/json",
        }
        if headers is not None:
            lambda_headers.update(**headers)
        return (body, status_code, lambda_headers)

    @staticmethod
    def build_errors(data, status_code=500, errors="", headers=None) -> tuple:
        errors = data
        body = {"data": None, "errors": errors}
        lambda_headers = {
            "Content-Type": "application/json",
        }
        if headers is not None:
            lambda_headers.update(**headers)
        return (body, status_code, lambda_headers)

    def initialize(self, config):
        self._config = config
        self._controller = None

    @property
    def controller(self):
        if not self._controller:
            self._controller = self._get_controller_class()()
        return self._controller

    def _get_controller_class(self):
        raise NotImplementedError


class BaseAuthApi(BaseApi):
    """
    This is base api which helps to validate the token
    """

    decorators = [ErrorCatcher, AuthTokenMiddleware, AuditLogger]
