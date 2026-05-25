from sqlalchemy.orm import sessionmaker, scoped_session
from sqlalchemy.ext.asyncio import async_sessionmaker, AsyncSession


def should_retry():
    return True


class BaseDB:
    def __init__(self):
        self._engine = None

    @staticmethod
    def session(engine):
        """
        scoped session is a factory that maps results of scopefunc to sessions
        so if scopefunc returns request object, scoped_session returns
        the same session for a single request,
        but different sessions for different requests.
        """
        return scoped_session(sessionmaker(bind=engine))

    @staticmethod
    def async_session(engine):
        """
        Async session factory for use with AsyncEngine.
        Returns an async_sessionmaker that produces AsyncSession instances.
        """
        return async_sessionmaker(bind=engine, class_=AsyncSession, expire_on_commit=False)

    @property
    def engine(self):
        if not self._engine:
            self._engine = self._get_engine()
        return self._engine

    def _get_engine(self):
        raise NotImplementedError
