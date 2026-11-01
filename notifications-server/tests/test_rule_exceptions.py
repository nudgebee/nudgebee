"""
A rule's exceptions leave findings out of it: a list of groups, where a finding is
left out when some group lists its value under every key the group has. The
editor writes one group per field, which reads as "leave out any of these".
"""

import asyncio
from types import SimpleNamespace

import pytest

from notifications_server.exceptions.notification_errors import InvalidRuleError
from notifications_server.services.message import NotificationRuleMatcher, _excluded, resolve_outcome
from notifications_server.services.rules import SCOPE_ACCOUNTS, SCOPE_ALL, normalize_exceptions
from tests.test_rule_account_scope import ACCT_A, ACCT_B, ACCT_C, TENANT, _db_rule, _save, _service, _Session

PROD, DEV = "acc-prod", "acc-dev"


def _rule(rule_id, exceptions=None, suppressed=False, accounts=None):
    return SimpleNamespace(
        id=rule_id,
        name=rule_id,
        source="troubleshoot",
        account_id=None,
        scope_mode=SCOPE_ACCOUNTS if accounts else SCOPE_ALL,
        account_ids=accounts or [],
        missing_columns=frozenset(),
        namespace=None,
        workload=None,
        aggregation_key=None,
        severity=None,
        is_active=True,
        is_suppressed=suppressed,
        expires_at=None,
        delivery_mode="real_time",
        event_types=None,
        exceptions=exceptions,
        created_at=None,
    )


# ----------------------------- matching -----------------------------


def test_any_group_leaves_a_finding_out():
    rule = _rule("r", exceptions=[{"account_ids": [PROD]}, {"namespaces": ["payments"]}])
    assert _excluded(rule, PROD, "default", None, None)
    assert _excluded(rule, DEV, "payments", None, None)
    assert not _excluded(rule, DEV, "default", None, None)


def test_every_key_of_a_group_must_match():
    rule = _rule("r", exceptions=[{"account_ids": [PROD], "namespaces": ["payments"]}])
    assert _excluded(rule, PROD, "payments", None, None)
    assert not _excluded(rule, PROD, "default", None, None)
    assert not _excluded(rule, DEV, "payments", None, None)


def test_a_finding_without_the_value_is_not_left_out():
    rule = _rule("r", exceptions=[{"workloads": ["api"]}])
    assert not _excluded(rule, PROD, "default", None, None)


@pytest.mark.parametrize("exceptions", [None, [], "garbage", [{}], [None], [{"namespaces": "payments"}]])
def test_empty_or_malformed_exceptions_leave_nothing_out(exceptions):
    assert not _excluded(_rule("r", exceptions=exceptions), PROD, "payments", "api", "alert")


class _RulesService:
    def __init__(self, rules):
        self._rules = rules

    async def get_notification_rules(self, _tenant_id):
        return self._rules


class _Mappings:
    def __init__(self, mappings):
        self._mappings = mappings

    async def execute(self, _statement):
        mappings = self._mappings
        return SimpleNamespace(scalars=lambda: SimpleNamespace(all=lambda: mappings))


def _outcome(rules, mappings, account):
    payload = {"source": "troubleshoot", "parameters": {"finding": {"cloud_account_id": account}}}
    matcher = NotificationRuleMatcher(_RulesService(rules))
    matched, source = asyncio.run(matcher.match_notification_rules(_Mappings(mappings), TENANT, "finding", payload))
    return resolve_outcome(matched, source=source)


def test_silence_everything_except_prod_and_route_prod():
    rules = [
        _rule("silence", exceptions=[{"account_ids": [PROD]}], suppressed=True),
        _rule("prod", accounts=[PROD]),
    ]
    mappings = [SimpleNamespace(rule_id="prod", platform="slack", channels={"id": "#prod-alerts"})]

    prod = _outcome(rules, mappings, PROD)
    assert prod.response is None
    assert [t["channels"] for t in prod.targets] == [{"id": "#prod-alerts"}]

    dev = _outcome(rules, mappings, DEV)
    assert dev.response[0]["status"] == "suppressed"


def test_an_exception_on_a_deliver_rule_leaves_the_finding_to_the_default():
    rules = [_rule("general", exceptions=[{"account_ids": [PROD]}])]
    mappings = [SimpleNamespace(rule_id="general", platform="slack", channels={"id": "#general"})]

    assert [t["channels"] for t in _outcome(rules, mappings, DEV).targets] == [{"id": "#general"}]
    left_out = _outcome(rules, mappings, PROD)
    assert (left_out.targets, left_out.use_defaults) == ([], True)


# ----------------------------- saving -----------------------------


def test_exceptions_are_cleaned():
    assert normalize_exceptions(None) == []
    assert normalize_exceptions([{"namespaces": [" payments ", "payments", "search"]}]) == [
        {"namespaces": ["payments", "search"]}
    ]


@pytest.mark.parametrize(
    "raw",
    [
        {"namespaces": ["x"]},
        [{}],
        [{"clusters": ["x"]}],
        [{"namespaces": []}],
        [{"namespaces": ["  "]}],
        [{"namespaces": "payments"}],
        [{"namespaces": [1]}],
        [{"namespaces": ["x"]}] * 21,
        [{"namespaces": [str(i) for i in range(101)]}],
    ],
)
def test_malformed_exceptions_are_rejected(raw):
    with pytest.raises(InvalidRuleError):
        normalize_exceptions(raw)


def _saved_rule(session):
    return next(o for o in session.added if hasattr(o, "exceptions"))


def test_an_all_accounts_rule_can_leave_tenant_accounts_out(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    service = _service(monkeypatch, session)

    result = _save(
        service,
        {
            "name": "Silence all",
            "source": "troubleshoot",
            "scope_mode": SCOPE_ALL,
            "is_suppressed": True,
            "exceptions": [{"account_ids": [ACCT_A]}],
        },
    )

    assert "error" not in result, result
    assert _saved_rule(session).exceptions == [{"account_ids": [ACCT_A]}]


def test_an_account_outside_the_tenant_is_rejected(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    service = _service(monkeypatch, session)
    result = _save(
        service,
        {
            "name": "Silence all",
            "source": "troubleshoot",
            "scope_mode": SCOPE_ALL,
            "exceptions": [{"account_ids": [ACCT_C]}],
        },
    )
    assert result["status_code"] == 400 and ACCT_C in result["error"]


def test_an_account_scoped_rule_cannot_leave_accounts_out(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod", ACCT_B: "k8s-dev"})
    service = _service(monkeypatch, session)
    result = _save(
        service,
        {"name": "Prod", "source": "troubleshoot", "account_ids": [ACCT_A], "exceptions": [{"account_ids": [ACCT_B]}]},
    )
    assert result["status_code"] == 400
    assert not session.committed


def test_a_batch_rule_cannot_have_exceptions(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    service = _service(monkeypatch, session)
    result = _save(
        service,
        {
            "name": "Digest",
            "source": "troubleshoot",
            "account_ids": [ACCT_A],
            "delivery_mode": "batch",
            "exceptions": [{"namespaces": ["payments"]}],
        },
    )
    assert result["status_code"] == 400


def test_an_update_that_omits_exceptions_keeps_them(monkeypatch):
    existing = _db_rule(SCOPE_ALL)
    existing.exceptions = [{"namespaces": ["payments"]}]
    session = _Session(rules=[existing])
    service = _service(monkeypatch, session)

    result = _save(
        service, {"id": str(existing.id), "name": "Prod alerts", "source": "troubleshoot", "is_suppressed": True}
    )

    assert "error" not in result, result
    assert existing.exceptions == [{"namespaces": ["payments"]}]


def test_an_empty_list_clears_exceptions(monkeypatch):
    existing = _db_rule(SCOPE_ALL)
    existing.exceptions = [{"namespaces": ["payments"]}]
    session = _Session(rules=[existing])
    service = _service(monkeypatch, session)

    _save(service, {"id": str(existing.id), "name": "Prod alerts", "source": "troubleshoot", "exceptions": []})

    assert existing.exceptions == []
