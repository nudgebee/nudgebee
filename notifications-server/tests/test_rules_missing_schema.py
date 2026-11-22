"""
Pods of a new release can start before its migration runs, so the rules model can
map a column the database does not have yet. Rule loading must keep every rule
working in that window (instead of returning none), and saves must fail with a
clear retryable error. The fake session reproduces Postgres behaviour after a
failed statement: nothing else runs until rollback.
"""

import asyncio
import uuid

from sqlalchemy.exc import DBAPIError, ProgrammingError
from sqlalchemy.sql.elements import TextClause

from notifications_server.models.models import NotificationRules
from notifications_server.services import rules as rules_module
from notifications_server.services.rules import NotificationRulesService
from notifications_server.services.cache import AsyncCache

TENANT = "22222222-2222-2222-2222-222222222222"
DB_COLUMNS_BEFORE_MIGRATION = [c.name for c in NotificationRules.__table__.columns if c.name != "event_types"]


class _PgError(Exception):
    def __init__(self, sqlstate):
        super().__init__(f"sqlstate {sqlstate}")
        self.sqlstate = sqlstate


def _missing_column_error():
    return ProgrammingError("SELECT notification_rules.event_types ...", {}, _PgError("42703"))


class _Rows:
    def __init__(self, rows):
        self._rows = rows

    def scalars(self):
        return self

    def all(self):
        return self._rows

    def __iter__(self):
        return iter(self._rows)


class _Session:
    """Fails the first full-model select like Postgres does for an unknown column."""

    def __init__(self, rules, first_error, db_columns=None):
        self._rules = rules
        self._first_error = first_error
        self._db_columns = DB_COLUMNS_BEFORE_MIGRATION if db_columns is None else db_columns
        self.aborted = False
        self.rolled_back = 0
        self.executed = []

    async def execute(self, statement, params=None):
        if self.aborted:
            raise DBAPIError("SELECT ...", {}, _PgError("25P02"))
        self.executed.append(statement)
        if isinstance(statement, TextClause):
            return _Rows([(name,) for name in self._db_columns])
        if "notification_rule_accounts" in str(statement):
            return _Rows([])
        if self._first_error is not None:
            error, self._first_error = self._first_error, None
            self.aborted = True
            raise error
        return _Rows(self._rules)

    async def rollback(self):
        self.aborted = False
        self.rolled_back += 1


class _SessionFactory:
    def __init__(self, session):
        self._session = session

    def __call__(self):
        return self

    async def __aenter__(self):
        return self._session

    async def __aexit__(self, *exc):
        return False


class _Cache:
    redis_client = object()
    aio = property(AsyncCache)

    def __init__(self):
        self.cached = []

    def get_notification_rules(self, tenant_id):
        return None

    def cache_notification_rules(self, tenant_id, rules):
        self.cached.append(rules)

    def delete_cached_notification_rules(self, tenant_id):
        pass


def _rule():
    return NotificationRules(
        id=uuid.uuid4(),
        tenant_id=TENANT,
        name="Prod alerts",
        source="troubleshoot",
        is_active=True,
        is_suppressed=True,
    )


def _service(monkeypatch, session):
    monkeypatch.setattr(rules_module.BaseDB, "async_session", staticmethod(lambda engine: _SessionFactory(session)))
    service = NotificationRulesService(engine=None)
    service.cache = _Cache()
    return service


def test_rules_still_load_when_a_mapped_column_is_missing(monkeypatch):
    rule = _rule()
    session = _Session([rule], _missing_column_error())
    service = _service(monkeypatch, session)

    loaded = asyncio.run(service.get_notification_rules(TENANT))

    assert loaded == [rule]
    assert loaded[0].event_types is None
    assert session.rolled_back == 1
    # The degraded shape is never cached; the next load after the migration reads the full row.
    assert service.cache.cached == []


def test_missing_table_loads_no_rules_without_querying_it(monkeypatch):
    table_error = ProgrammingError("SELECT ...", {}, _PgError("42P01"))
    session = _Session([_rule()], table_error, db_columns=[])
    service = _service(monkeypatch, session)

    assert asyncio.run(service.get_notification_rules(TENANT)) == []
    assert session.rolled_back == 1
    # Only the failed select and the column lookup ran; nothing tried to select from a missing table again.
    assert len(session.executed) == 2
    assert service.cache.cached == []


def test_other_query_errors_are_not_masked(monkeypatch):
    session = _Session([_rule()], ProgrammingError("SELECT ...", {}, _PgError("42601")))
    service = _service(monkeypatch, session)

    assert asyncio.run(service.get_notification_rules(TENANT)) == []
    assert not any(isinstance(s, TextClause) for s in session.executed)


def test_full_load_is_cached_as_before(monkeypatch):
    rule = _rule()
    session = _Session([rule], None)
    service = _service(monkeypatch, session)

    assert asyncio.run(service.get_notification_rules(TENANT)) == [rule]
    assert service.cache.cached == [[rule]]
    assert session.rolled_back == 0


def test_update_before_migration_returns_a_retryable_error(monkeypatch):
    session = _Session([_rule()], _missing_column_error())
    service = _service(monkeypatch, session)

    result = asyncio.run(
        service.save_or_update_rule(
            action={"name": "notifications_upsert_rule"},
            input={
                "rule": {
                    "id": str(uuid.uuid4()),
                    "name": "Prod alerts",
                    "source": "troubleshoot",
                    "account_id": str(uuid.uuid4()),
                    "is_suppressed": False,
                }
            },
            session_variables={"tenant_id": TENANT, "user_id": str(uuid.uuid4())},
        )
    )

    assert result["status_code"] == 503
    # Nothing else ran on the aborted transaction; closing the session rolls it back.
    assert len(session.executed) == 1
