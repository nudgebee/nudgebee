from sqlalchemy.dialects import postgresql
from sqlalchemy.orm import Query, Session

from notifications_server.repositories.user_repository import get_user_by_email, get_user_tenants


def _capture_sql(monkeypatch, method):
    captured = []

    def run(query):
        captured.append(
            str(query.statement.compile(dialect=postgresql.dialect(), compile_kwargs={"literal_binds": True}))
        )
        return [] if method == "all" else None

    monkeypatch.setattr(Query, method, run)
    return captured


def test_get_user_tenants_skips_suspended_users(monkeypatch):
    captured = _capture_sql(monkeypatch, "all")

    assert get_user_tenants(Session(), "someone@example.com") == (None, None)

    sql = captured[0]
    assert "users.status IS NULL OR users.status != 'suspended'" in sql
    assert "users.username = 'someone@example.com'" in sql


def test_get_user_by_email_skips_suspended_users(monkeypatch):
    captured = _capture_sql(monkeypatch, "first")

    assert get_user_by_email(Session(), " someone@example.com ") is None

    sql = captured[0]
    assert "users.status IS NULL OR users.status != 'suspended'" in sql
    assert "users.username = 'someone@example.com'" in sql
