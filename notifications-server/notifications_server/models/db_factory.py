import logging
from enum import Enum

from notifications_server.exceptions.common_exc import InvalidModelTypeException
from notifications_server.exceptions.exceptions import Err
from notifications_server.models.db_postgres import PostgresSQLDB


class DBType(Enum):
    Test = "test"
    MySQL = "mysql"
    PostgresSQL = "postgresql"


LOG = logging.getLogger(__name__)


class DBFactory:
    DBS = {DBType.PostgresSQL: PostgresSQLDB}
    _instances = {}

    @staticmethod
    def _get_db(db_type):
        db_class = DBFactory.DBS.get(db_type)
        if not db_class:
            LOG.error("Nonexistent model type specified: %s", db_type)
            raise InvalidModelTypeException(Err.OS0001, [db_type])
        else:
            return db_class()

    def __new__(cls, db_type, *args, **kwargs):
        if db_type not in cls._instances:
            instance = super().__new__(cls, *args, **kwargs)
            instance._db = DBFactory._get_db(db_type)
            cls._instances[db_type] = instance
        return cls._instances[db_type]

    @classmethod
    def clean_type(cls, db_type):
        if cls._instances.get(db_type):
            del cls._instances[db_type]

    @property
    def db(self):
        return self._db
