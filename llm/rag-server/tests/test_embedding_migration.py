"""Re-embedding migration: what gets registered, and in what order it is rebuilt.

Two properties carry the weight here.

Registration must key on the model NAME, not the dimension. Different models
routinely share one -- bge-small-en-v1.5 and all-MiniLM-L6-v2 are both 384 -- so
a dimension check would call a swap between them "already correct" and leave
every vector silently wrong.

Rebuild ordering must stage before it deletes. Document text lives only in the
Qdrant payload, so dropping the original before the replacement holds the data
destroys it with nothing to restore from.
"""

import contextlib

import pytest
from qdrant_client.models import Distance, VectorParams

from rag.core.documents import embedding_migration as em


@pytest.fixture
def _holds_lock(monkeypatch):
    """Grant the cross-replica lock. Tests of run() are about what gets rebuilt;
    the locking itself is exercised separately below."""

    @contextlib.contextmanager
    def _granted():
        yield lambda: True

    monkeypatch.setattr(em, "_exclusive_run", _granted)


class _Coll:
    def __init__(self, dim, points, metadata=None):
        self.dim = dim
        self.points = points
        self.metadata = metadata or {}


class _FakeClient:
    """Minimal Qdrant stand-in that records the order of destructive calls."""

    def __init__(self, collections):
        self.colls = collections
        self.events = []

    def get_collections(self):
        return type("R", (), {"collections": [type("C", (), {"name": n})() for n in list(self.colls)]})()

    def get_collection(self, name):
        if name not in self.colls:
            raise KeyError(name)
        c = self.colls[name]
        params = VectorParams(size=c.dim, distance=Distance.COSINE)
        cfg = type("Cfg", (), {"params": type("P", (), {"vectors": params})(), "metadata": c.metadata})()
        return type("Info", (), {"config": cfg, "points_count": len(c.points)})()

    def create_collection(self, name, vectors_config, metadata=None):
        self.events.append(("create", name))
        self.colls[name] = _Coll(vectors_config.size, [], metadata or {})

    def delete_collection(self, name):
        self.events.append(("delete", name))
        self.colls.pop(name, None)

    def scroll(self, collection_name, limit, offset=None, with_payload=True, with_vectors=False):
        pts = self.colls[collection_name].points
        start = offset or 0
        window = pts[start : start + limit]
        return window, (start + limit if start + limit < len(pts) else None)

    def upsert(self, collection_name, points, wait=True):
        self.colls[collection_name].points.extend(points)


class _Point:
    def __init__(self, pid, text, vector=None):
        self.id = pid
        self.payload = {"page_content": text}
        self.vector = vector or [0.0]


class _Embeddings:
    def __init__(self, dim, name):
        self.dim = dim
        self.model_id = name
        self.calls = 0

    def embed_query(self, _t):
        return [0.1] * self.dim

    def embed_documents(self, texts):
        self.calls += len(texts)
        return [[0.1] * self.dim for _ in texts]


def _stamped(dim, model, n=3):
    return _Coll(dim, [_Point(i, f"doc {i}") for i in range(n)], {em.MODEL_KEY: model})


def test_account_id_is_taken_from_the_collection_name():
    acct = "a2a30b02-0f67-42e5-a2ab-c658230fd798"
    assert em._account_of(f"{acct}_knowledge_base") == acct


@pytest.mark.parametrize("name", ["nudgebee_docs", "kubectl", "events", "kb_not-a-uuid"])
def test_global_collections_have_no_account(name):
    assert em._account_of(name) == ""


def test_same_model_is_not_registered():
    client = _FakeClient({"c": _stamped(384, "bge-small-en-v1.5")})
    assert em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5")) == []


def test_same_dimension_different_model_is_still_registered():
    """The reason the model name is stored at all: 384 != 384 is not enough."""
    client = _FakeClient({"c": _stamped(384, "all-MiniLM-L6-v2")})
    pending = em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5"))
    assert [p.name for p in pending] == ["c"]
    assert pending[0].stored_model == "all-MiniLM-L6-v2"


def test_unstamped_collection_at_the_expected_dimension_is_left_alone():
    """Predates the stamp; same dimension is the best evidence available, and
    rebuilding every legacy collection on a hunch costs hours."""
    client = _FakeClient({"c": _Coll(384, [_Point(1, "x")])})
    assert em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5")) == []


def test_unstamped_collection_at_a_different_dimension_is_registered():
    client = _FakeClient({"c": _Coll(3072, [_Point(1, "x")])})
    pending = em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5"))
    assert [p.name for p in pending] == ["c"]
    assert pending[0].stored_model == "unstamped"


def test_a_staging_collection_is_registered_under_its_source_name_not_its_own():
    """Staging is never a migration target in its own right. A lone one means
    the original was already dropped, so the source is registered for restore
    -- earlier this returned nothing and the data stayed orphaned."""
    client = _FakeClient({f"c{em.STAGING_SUFFIX}": _Coll(3072, [_Point(1, "x")])})
    pending = em.register(client, lambda _a: _Embeddings(384, "m"))

    assert [p.name for p in pending] == ["c"]
    assert all(not p.name.endswith(em.STAGING_SUFFIX) for p in pending)


def test_original_is_only_deleted_after_staging_holds_the_data():
    client = _FakeClient({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(5)])})
    p = em.register(client, lambda _a: _Embeddings(384, "new"))[0]
    em.rebuild(client, p, lambda: _Embeddings(384, "new"))

    staged = client.events.index(("create", f"c{em.STAGING_SUFFIX}"))
    dropped = client.events.index(("delete", "c"))
    assert staged < dropped, "staging must exist before the original is dropped"


def test_rebuilt_collection_keeps_every_point_and_records_the_model():
    client = _FakeClient({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(7)])})
    p = em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5"))[0]
    em.rebuild(client, p, lambda: _Embeddings(384, "bge-small-en-v1.5"))

    assert len(client.colls["c"].points) == 7
    assert client.colls["c"].dim == 384
    assert client.colls["c"].metadata[em.MODEL_KEY] == "bge-small-en-v1.5"
    assert f"c{em.STAGING_SUFFIX}" not in client.colls, "staging should be cleaned up"


def test_rebuild_keeps_the_metadata_that_decides_which_searches_reach_it():
    """module/account/tenant_id select which collections a search covers. A
    rebuild that recreated the collection without them would leave the data
    intact and unreachable, which is worse than losing it loudly."""
    original = {
        "module": "knowledge_base",
        "account": "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        "tenant_id": "890cad87-c452-4aa7-b84a-742cee0454a1",
        "source": "confluence",
    }
    client = _FakeClient({"c": _Coll(3072, [_Point(1, "x")], dict(original))})
    p = em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5"))[0]
    em.rebuild(client, p, lambda: _Embeddings(384, "bge-small-en-v1.5"))

    after = client.colls["c"].metadata
    for k, v in original.items():
        assert after.get(k) == v, f"{k} was dropped by the rebuild"
    assert after[em.MODEL_KEY] == "bge-small-en-v1.5"


def test_rebuilt_collection_is_not_registered_again():
    client = _FakeClient({"c": _Coll(3072, [_Point(1, "x")])})
    embs = _Embeddings(384, "bge-small-en-v1.5")
    em.rebuild(client, em.register(client, lambda _a: embs)[0], lambda: embs)
    assert em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5")) == []


def test_interrupted_run_reuses_staging_instead_of_re_embedding():
    staged = [_Point(i, f"doc {i}", vector=[0.1] * 384) for i in range(4)]
    client = _FakeClient(
        {
            "c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(4)]),
            f"c{em.STAGING_SUFFIX}": _Coll(384, staged),
        }
    )
    p = em.register(client, lambda _a: _Embeddings(384, "new"))[0]
    emb = _Embeddings(384, "new")
    em.rebuild(client, p, lambda: emb)
    assert emb.calls == 0, "a complete staging collection must not be re-embedded"


def test_each_account_is_measured_against_its_own_model(_holds_lock):
    """An account with its own embeddings integration keeps its own model;
    rebuilding it with the deployment default would strand its queries."""
    a = "11111111-1111-1111-1111-111111111111"
    b = "22222222-2222-2222-2222-222222222222"
    client = _FakeClient(
        {
            f"{a}_knowledge_base": _stamped(3072, "gemini-embedding-001"),
            f"{b}_knowledge_base": _stamped(3072, "gemini-embedding-001"),
        }
    )
    models = {a: _Embeddings(384, "bge-small-en-v1.5"), b: _Embeddings(3072, "gemini-embedding-001")}
    summary = em.run(client, lambda acct: models[acct])
    assert summary["registered"] == 1, "only the account that changed model is rebuilt"
    assert summary["rebuilt"] == 1
    assert models[b].calls == 0, "the unchanged account pays nothing"


def test_one_failing_collection_does_not_strand_the_others(_holds_lock):
    class _Boom(_FakeClient):
        def scroll(self, collection_name, **kw):
            if collection_name == "bad":
                raise RuntimeError("qdrant is unhappy")
            return super().scroll(collection_name, **kw)

    client = _Boom({"bad": _Coll(3072, [_Point(1, "x")]), "good": _Coll(3072, [_Point(2, "y")])})
    summary = em.run(client, lambda _a: _Embeddings(384, "new"))
    assert summary["failed"] == 1
    assert summary["rebuilt"] == 1


def test_run_on_a_clean_store_does_nothing(_holds_lock):
    client = _FakeClient({"c": _stamped(384, "m")})
    assert em.run(client, lambda _a: _Embeddings(384, "m")) == {"registered": 0, "rebuilt": 0, "failed": 0}


def test_background_run_never_raises_into_the_caller():
    """A daemon thread that propagates would take nothing down but would leave
    an unhandled-exception traceback and no retry."""

    class _Broken(_FakeClient):
        def get_collections(self):
            raise RuntimeError("qdrant unreachable at startup")

    thread = em.start_background(_Broken({}), lambda _a: _Embeddings(384, "m"), retry_delay=0)
    thread.join(timeout=10)
    assert not thread.is_alive(), "the worker must exit rather than hang"


def test_migration_is_off_unless_explicitly_enabled(monkeypatch):
    monkeypatch.delenv("RAG_EMBEDDING_MIGRATION_ENABLED", raising=False)
    assert em.is_enabled() is False
    monkeypatch.setenv("RAG_EMBEDDING_MIGRATION_ENABLED", "true")
    assert em.is_enabled() is True


def test_a_second_replica_rebuilds_nothing_while_another_holds_the_lock(monkeypatch):
    """Replicas share one Qdrant. Two rebuilding the same collection would have
    one delete the original while the other is still reading it."""

    @contextlib.contextmanager
    def _denied():
        yield None

    monkeypatch.setattr(em, "_exclusive_run", _denied)
    client = _FakeClient({"c": _Coll(3072, [_Point(1, "x")])})
    summary = em.run(client, lambda _a: _Embeddings(384, "new"))

    assert summary["rebuilt"] == 0
    assert summary["skipped_locked"] == 1
    assert client.events == [], "a replica without the lock must not touch any collection"


def test_a_database_failure_is_raised_not_reported_as_a_healthy_skip():
    """ "Another replica has it" and "the database is down" must not look the
    same. Collapsing them lets a blip read as a successful skip, and with the
    provider already switched every search then returns nothing -- permanently,
    because nothing retries."""

    class _DeadEngine:
        def connect(self):
            raise RuntimeError("database unreachable")

    mods = __import__("sys").modules
    saved = mods.get("rag.core.utils.db_query")
    mods["rag.core.utils.db_query"] = type("m", (), {"engine": _DeadEngine()})
    try:
        client = _FakeClient({"c": _Coll(3072, [_Point(1, "x")])})
        with pytest.raises(em.MigrationUnavailable):
            em.run(client, lambda _a: _Embeddings(384, "new"))
        assert client.events == [], "an unguarded run must not start"
    finally:
        if saved is not None:
            mods["rag.core.utils.db_query"] = saved
        else:
            mods.pop("rag.core.utils.db_query", None)


def test_a_short_embedding_batch_stops_the_collection_instead_of_dropping_points(_holds_lock):
    """zip() would pair payloads with the wrong vectors or silently drop them,
    and the original is deleted once staging looks complete."""

    class _ShortEmbeddings(_Embeddings):
        def embed_documents(self, texts):
            return [[0.1] * self.dim for _ in texts][:-1]  # one short

    client = _FakeClient({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(3)])})
    summary = em.run(client, lambda _a: _ShortEmbeddings(384, "new"))

    assert summary["rebuilt"] == 0
    assert summary["failed"] == 1
    assert "c" in client.colls, "the original must survive a failed rebuild"
    assert len(client.colls["c"].points) == 3


def test_losing_the_lock_mid_run_stops_before_the_next_collection(monkeypatch):
    """A dropped connection releases the advisory lock, so another replica may
    already be working. Continuing would be the very race the lock prevents."""
    done = {"n": 0}
    real_rebuild = em.rebuild

    def counting_rebuild(client, p, load_embeddings, still_held=lambda: True):
        result = real_rebuild(client, p, load_embeddings, still_held)
        done["n"] += 1
        return result

    monkeypatch.setattr(em, "rebuild", counting_rebuild)

    @contextlib.contextmanager
    def _lock_dies_after_one_collection():
        yield lambda: done["n"] < 1

    monkeypatch.setattr(em, "_exclusive_run", _lock_dies_after_one_collection)
    client = _FakeClient(
        {
            "a": _Coll(3072, [_Point(1, "x")]),
            "b": _Coll(3072, [_Point(2, "y")]),
        }
    )
    summary = em.run(client, lambda _a: _Embeddings(384, "new"))

    assert summary["rebuilt"] == 1
    assert summary["stopped_lock_lost"] == 1
    assert client.colls["b"].dim == 3072, "the second collection must be left untouched"


def test_an_error_inside_the_lock_is_not_masked_by_the_context_manager():
    """Handling acquisition around the yield would catch exceptions thrown back
    in from the body and yield twice, raising "generator didn't stop after
    throw()" and hiding the real failure."""

    class _Result:
        def scalar(self):
            return True

    class _Conn:
        def execution_options(self, **_kw):
            return self

        def execute(self, *_a, **_kw):
            return _Result()

        def close(self):
            pass

    mods = __import__("sys").modules
    saved = mods.get("rag.core.utils.db_query")
    mods["rag.core.utils.db_query"] = type("m", (), {"engine": type("e", (), {"connect": lambda self: _Conn()})()})
    try:
        with pytest.raises(ValueError, match="boom"):
            with em._exclusive_run() as held:
                assert held is not None
                raise ValueError("boom")
    finally:
        if saved is not None:
            mods["rag.core.utils.db_query"] = saved
        else:
            mods.pop("rag.core.utils.db_query", None)


def test_a_crash_during_the_final_copy_is_picked_up_again(_holds_lock):
    """The dangerous window: the rebuilt collection exists and is already
    stamped with the new model, but holds only some of the points. Trusting the
    stamp would skip it forever and the missing points are unrecoverable."""
    client = _FakeClient(
        {
            # what a crash mid-copy leaves behind
            "c": _Coll(384, [_Point(1, "x")], {em.MODEL_KEY: "bge-small-en-v1.5"}),
            f"c{em.STAGING_SUFFIX}": _Coll(384, [_Point(i, f"doc {i}") for i in range(4)]),
        }
    )
    pending = em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5"))

    assert [p.name for p in pending] == ["c"], "leftover staging must force a rebuild"


def test_an_account_whose_provider_is_down_is_asked_once_not_once_per_collection(_holds_lock):
    acct = "33333333-3333-3333-3333-333333333333"
    client = _FakeClient({f"{acct}_{m}": _Coll(3072, [_Point(1, "x")]) for m in ("a", "b", "c")})
    calls = {"n": 0}

    def _down(_account):
        calls["n"] += 1
        raise RuntimeError("provider unreachable")

    assert em.register(client, _down) == []
    assert calls["n"] == 1, "a dead provider must be asked once per account, not once per collection"


@pytest.mark.parametrize("payload", [None, "not a dict", 42, []])
def test_a_payload_of_an_unexpected_shape_yields_no_text_rather_than_raising(payload):
    """Payloads written by older ingests are read here; a surprise should skip
    the point, not abort the rebuild."""
    assert em._text_of(payload) == ""


def test_non_string_page_content_is_coerced_rather_than_crashing():
    assert em._text_of({"page_content": 12345}) == "12345"


def test_staging_whose_original_was_already_deleted_is_restored(_holds_lock):
    """The narrowest and worst window: the run died between dropping the
    original and recreating it, so staging holds the only copy. Iterating
    active collections alone would never look at it and the data is gone."""
    client = _FakeClient(
        {
            f"c{em.STAGING_SUFFIX}": _Coll(
                384,
                [_Point(i, f"doc {i}", vector=[0.1] * 384) for i in range(4)],
                {"module": "knowledge_base", em.MODEL_KEY: "bge-small-en-v1.5"},
            )
        }
    )
    pending = em.register(client, lambda _a: _Embeddings(384, "bge-small-en-v1.5"))
    assert [p.name for p in pending] == ["c"], "an orphaned staging must be registered for restore"

    em.rebuild(client, pending[0], lambda: _Embeddings(384, "bge-small-en-v1.5"))

    assert "c" in client.colls, "the collection must be restored"
    assert len(client.colls["c"].points) == 4
    assert client.colls["c"].metadata["module"] == "knowledge_base"
    assert f"c{em.STAGING_SUFFIX}" not in client.colls


def test_restoring_an_orphan_does_not_re_embed(_holds_lock):
    """Staging already holds finished vectors; paying for them twice would turn
    a crash into hours of extra work."""
    client = _FakeClient(
        {
            f"c{em.STAGING_SUFFIX}": _Coll(
                384, [_Point(i, f"doc {i}", vector=[0.1] * 384) for i in range(3)], {em.MODEL_KEY: "m"}
            )
        }
    )
    p = em.register(client, lambda _a: _Embeddings(384, "m"))[0]
    emb = _Embeddings(384, "m")
    em.rebuild(client, p, lambda: emb)
    assert emb.calls == 0


def test_losing_the_lock_mid_rebuild_stops_before_destroying_the_original():
    """Checking only between collections leaves the worst window open: one
    collection can take hours, and the swap deletes the original."""
    client = _FakeClient({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(3)])})
    p = em.register(client, lambda _a: _Embeddings(384, "new"))[0]

    with pytest.raises(RuntimeError, match="migration lock lost"):
        em.rebuild(client, p, lambda: _Embeddings(384, "new"), still_held=lambda: False)

    assert "c" in client.colls, "the original must survive"
    assert client.colls["c"].dim == 3072
    assert len(client.colls["c"].points) == 3


def test_the_lock_is_rechecked_immediately_before_the_swap():
    """Staging can take hours to fill. The check that matters is the one taken
    just before the original is deleted, not the one taken before it started."""
    client = _FakeClient({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(3)])})
    p = em.register(client, lambda _a: _Embeddings(384, "new"))[0]

    def lock_dies_once_staging_is_full():
        staged = client.colls.get(f"c{em.STAGING_SUFFIX}")
        return not (staged and len(staged.points) >= 3)

    with pytest.raises(RuntimeError, match="migration lock lost"):
        em.rebuild(client, p, lambda: _Embeddings(384, "new"), still_held=lock_dies_once_staging_is_full)

    assert "c" in client.colls, "the original must not be deleted after the lock is lost"
    assert client.colls["c"].dim == 3072
    assert len(client.colls["c"].points) == 3


def test_losing_the_lock_on_the_last_collection_is_reported_as_a_stop_not_a_failure(monkeypatch):
    """The pre-loop check only runs before the *next* collection, so a lock
    lost on the final one would otherwise be recorded as a failed rebuild with
    a misleading traceback, and the stop would never be reported at all."""
    lost = {"yes": False}

    @contextlib.contextmanager
    def _lock_dies_during_the_only_collection():
        def held():
            staged = client.colls.get(f"c{em.STAGING_SUFFIX}")
            if staged and len(staged.points) >= 2:
                lost["yes"] = True
            return not lost["yes"]

        yield held

    monkeypatch.setattr(em, "_exclusive_run", _lock_dies_during_the_only_collection)
    client = _FakeClient({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(2)])})
    summary = em.run(client, lambda _a: _Embeddings(384, "new"))

    assert summary["stopped_lock_lost"] == 1
    assert summary["failed"] == 0, "a deliberate stop must not be counted as a failed collection"
    assert summary["rebuilt"] == 0
    assert "c" in client.colls, "the original must survive"


def test_a_reconnected_session_is_treated_as_having_lost_the_lock(monkeypatch):
    """A pool that transparently reconnects answers queries perfectly well
    while holding no advisory lock. A liveness ping reads that as success; only
    asking Postgres who holds the lock catches it."""
    state = {"held": True}

    class _Result:
        def __init__(self, v):
            self.v = v

        def scalar(self):
            return self.v

    class _Conn:
        def execution_options(self, **_kw):
            return self

        def execute(self, stmt, params=None):
            sql = str(stmt)
            if "pg_try_advisory_lock" in sql:
                return _Result(True)
            if "pg_locks" in sql:
                return _Result(state["held"])
            return _Result(None)

        def close(self):
            pass

    monkeypatch.setitem(
        __import__("sys").modules,
        "rag.core.utils.db_query",
        type("m", (), {"engine": type("e", (), {"connect": lambda self: _Conn()})()}),
    )

    with em._exclusive_run() as still_held:
        assert still_held is not None and still_held(), "the lock starts out held"
        state["held"] = False  # the pool silently reconnected
        assert not still_held(), "a session that no longer holds the lock must report lost"


def test_restoring_an_orphan_never_touches_the_embedding_provider(_holds_lock):
    """Staging already holds finished vectors. Recovery is exactly when the
    provider is most likely to be the thing that is broken, so asking it for a
    model that will not be used would fail the restore for no reason."""
    client = _FakeClient(
        {
            f"c{em.STAGING_SUFFIX}": _Coll(
                384, [_Point(i, f"doc {i}", vector=[0.1] * 384) for i in range(3)], {em.MODEL_KEY: "m"}
            )
        }
    )
    p = em.register(client, lambda _a: _Embeddings(384, "m"))[0]

    def _provider_is_down():
        raise RuntimeError("provider unreachable")

    em.rebuild(client, p, _provider_is_down)

    assert len(client.colls["c"].points) == 3, "the restore must complete without the provider"


def test_resuming_complete_staging_never_touches_the_embedding_provider(_holds_lock):
    client = _FakeClient(
        {
            "c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(2)]),
            f"c{em.STAGING_SUFFIX}": _Coll(
                384, [_Point(i, f"doc {i}", vector=[0.1] * 384) for i in range(2)], {em.MODEL_KEY: "new"}
            ),
        }
    )
    p = em.register(client, lambda _a: _Embeddings(384, "new"))[0]

    def _provider_is_down():
        raise RuntimeError("provider unreachable")

    em.rebuild(client, p, _provider_is_down)

    assert len(client.colls["c"].points) == 2


def test_the_background_worker_retries_a_transient_failure(monkeypatch):
    """Running once means a rolling update that briefly takes Postgres or
    Qdrant away abandons the rebuild for the life of the pod."""
    attempts = {"n": 0}

    def flaky(_client, _embeddings_for):
        attempts["n"] += 1
        if attempts["n"] < 3:
            raise RuntimeError("qdrant unreachable")
        return {"registered": 0, "rebuilt": 0, "failed": 0}

    monkeypatch.setattr(em, "run", flaky)
    em.start_background(_FakeClient({}), lambda _a: None, retry_delay=0).join(timeout=5)

    assert attempts["n"] == 3, "it must keep trying until a run succeeds"


def test_the_background_worker_gives_up_rather_than_retrying_forever(monkeypatch):
    attempts = {"n": 0}

    def always_broken(_client, _embeddings_for):
        attempts["n"] += 1
        raise RuntimeError("still unreachable")

    monkeypatch.setattr(em, "run", always_broken)
    thread = em.start_background(_FakeClient({}), lambda _a: None, retry_delay=0)
    thread.join(timeout=5)

    assert not thread.is_alive()
    assert attempts["n"] == em.RETRY_ATTEMPTS


def test_a_run_skipped_because_another_replica_holds_the_lock_is_not_retried(monkeypatch):
    """That is a healthy outcome — the other replica is doing the work."""
    attempts = {"n": 0}

    def skipped(_client, _embeddings_for):
        attempts["n"] += 1
        return {"registered": 0, "rebuilt": 0, "failed": 0, "skipped_locked": 1}

    monkeypatch.setattr(em, "run", skipped)
    em.start_background(_FakeClient({}), lambda _a: None, retry_delay=0).join(timeout=5)

    assert attempts["n"] == 1


def test_a_failed_collection_is_retried_rather_than_left_until_the_next_restart(monkeypatch):
    """A run that finishes with failures is not a finished run: those
    collections are unsearchable, and waiting for a restart is not a plan."""
    calls = {"n": 0}

    def one_failure_then_clean(_client, _embeddings_for):
        calls["n"] += 1
        if calls["n"] == 1:
            return {"registered": 1, "rebuilt": 0, "failed": 1}
        return {"registered": 0, "rebuilt": 0, "failed": 0}

    monkeypatch.setattr(em, "run", one_failure_then_clean)
    em.start_background(_FakeClient({}), lambda _a: None, retry_delay=0).join(timeout=5)

    assert calls["n"] == 2, "a run reporting failures must be retried"


def test_a_staging_cleanup_failure_does_not_undo_a_completed_rebuild(_holds_lock):
    """By this point the collection is rebuilt and correct. Failing the whole
    migration over a leftover temporary collection would retry finished work."""

    class _CleanupFails(_FakeClient):
        def delete_collection(self, name):
            if name.endswith(em.STAGING_SUFFIX) and name in self.colls and self.colls[name].points:
                raise RuntimeError("qdrant timeout on cleanup")
            return super().delete_collection(name)

    client = _CleanupFails({"c": _Coll(3072, [_Point(i, f"doc {i}") for i in range(3)])})
    p = em.register(client, lambda _a: _Embeddings(384, "new"))[0]

    em.rebuild(client, p, lambda: _Embeddings(384, "new"))

    assert client.colls["c"].dim == 384, "the rebuild must stand"
    assert len(client.colls["c"].points) == 3
