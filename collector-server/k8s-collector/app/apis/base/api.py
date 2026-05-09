from flask_restx import Resource

from middleware.auth_token_middleware import AuthTokenMiddleware, ErrorCatcher, AuditLogger


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
