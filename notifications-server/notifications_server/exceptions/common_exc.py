import requests
from fastapi import HTTPException


class BeeException(Exception):
    def __init__(self, error_code, params):
        """
        Creates a new exception

        :type error_code: Enum
        :type params: list
        """
        reason = error_code.value[0]
        reason = reason % tuple(params)
        super().__init__(reason)
        self.reason = reason
        self.err_code = error_code
        self.error_code = error_code.name
        self.params = params


class InvalidModelTypeException(BeeException):
    pass


class HeraldException(BeeException):
    pass


class WrongArgumentsException(BeeException):
    pass


class NotFoundException(BeeException):
    pass


class ConflictException(BeeException):
    pass


class UnauthorizedException(BeeException):
    pass


class ForbiddenException(BeeException):
    pass


class FailedDependency(BeeException):
    pass


class BadRequestException(BeeException):
    pass


class TimeoutException(BeeException):
    pass


class InternalServerError(BeeException):
    pass


class BeeHTTPError(HTTPException):
    def __init__(self, status_code, error_code, params):
        """
        Creates a new HTTP error

        :type status_code: int
        :type error_code: Enum
        :type params: list
        """
        reason = error_code.value[0]
        reason = reason % tuple(params)
        super().__init__(status_code=status_code, detail=reason)
        self.error_code = error_code.name
        self.reason = reason
        self.params = params

    @classmethod
    def from_opt_exception(cls, status_code, opt_exception):
        """
        Creates a new HTTP error from provided exception

        :type status_code: int
        :type opt_exception: BeeException
        :rtype: BeeHTTPError
        """
        return cls(status_code, opt_exception.err_code, opt_exception.params)


def handle503(f):
    def wrapped(*args, **kwargs):
        try:
            result = f(*args, **kwargs)
        except requests.exceptions.HTTPError as exc:
            if exc.response.status_code == 503:
                raise HTTPException(503, detail="Service Unavailable")
            raise
        return result

    return wrapped
