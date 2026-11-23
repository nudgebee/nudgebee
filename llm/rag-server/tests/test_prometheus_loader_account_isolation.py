"""One account's poisoned prometheus collection must not skip every account after it."""

from contextlib import nullcontext

from rag.core.documents.loaders import observability


class _Cursor:
    def close(self):
        pass


class _Connection:
    def cursor(self):
        return _Cursor()

    def close(self):
        pass


class _Engine:
    def raw_connection(self):
        return _Connection()

    def dispose(self):
        pass


def test_load_prom_json_docs_continues_past_a_failing_account(monkeypatch):
    loaded = []

    def handle(account_id, module):
        if account_id == "poisoned":
            raise TypeError("unhashable type: 'dict'")
        loaded.append(account_id)

    monkeypatch.setattr(observability, "create_engine", lambda url: _Engine())
    monkeypatch.setattr(observability, "get_active_accounts", lambda cursor: [("poisoned",), ("second",), ("third",)])
    monkeypatch.setattr(observability, "load_and_filter_files", lambda paths: [])
    monkeypatch.setattr(observability, "handle_account_specific_docs", handle)
    monkeypatch.setattr(observability, "closing", nullcontext)

    observability.load_prom_json_docs(force=True)

    assert loaded == ["second", "third"]
