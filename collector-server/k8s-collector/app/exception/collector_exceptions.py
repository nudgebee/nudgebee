from werkzeug.exceptions import HTTPException


class NotFoundError(HTTPException):
    code = 404
    name = "NotFound"


class UnauthorizedError(HTTPException):
    code = 401
    name = "Unauthorized"


class InvalidTokenError(HTTPException):
    code = 403
    name = "InvalidTokenError"


class BadRequestError(HTTPException):
    code = 400
    name = "BadRequestError"


class InternalServerError(HTTPException):
    code = 500
    name = "InternalServerError"
