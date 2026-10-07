import json

import pytest

from memory_golden.run import extract_writes, parse_providers
from memory_golden.scoring import (
    CaseResult,
    ProviderSummary,
    Write,
    name_matches,
    fold,
    render_repeats,
    score_case,
)

PREF = {"id": "p", "expect": "preference", "must_contain": ["prod eu west"]}
NONE = {"id": "n", "expect": "none"}


def test_fold_matches_memory_read():
    assert fold("Prod-EU_west  ") == "prod eu west"


@pytest.mark.parametrize(
    "writes,outcome",
    [
        ([], "missed"),
        ([Write({"type": "preference", "key": "cluster"}, accepted=False)], "refused"),
        (
            [Write({"type": "fact", "subject": "prod-eu-west"}, accepted=True)],
            "type_mixup",
        ),
        (
            [
                Write(
                    {"type": "preference", "key": "cluster", "value": "staging"},
                    accepted=True,
                )
            ],
            "wrong_content",
        ),
        (
            [
                Write(
                    {
                        "type": "preference",
                        "key": "default_cluster",
                        "value": "prod_eu_west",
                    },
                    accepted=True,
                )
            ],
            "correct",
        ),
    ],
)
def test_score_expected_save(writes, outcome):
    assert score_case(PREF, writes) == outcome


def test_evidence_does_not_count_as_content():
    w = Write(
        {
            "type": "preference",
            "key": "cluster",
            "value": "x",
            "evidence": "prod-eu-west",
        },
        accepted=True,
    )
    assert score_case(PREF, [w]) == "wrong_content"


def test_must_contain_any():
    case = {"id": "a", "expect": "preference", "must_contain_any": ["bullet", "short"]}
    assert (
        score_case(
            case,
            [Write({"type": "preference", "key": "style", "value": "short"}, True)],
        )
        == "correct"
    )


def test_negative_case():
    assert score_case(NONE, []) == "quiet"
    assert score_case(NONE, [Write({"type": "fact"}, accepted=False)]) == "refused_save"
    assert score_case(NONE, [Write({"type": "fact"}, accepted=True)]) == "false_save"


def test_summary_numbers_and_verdict():
    s = ProviderSummary("p")
    s.results = [CaseResult(f"s{i}", "preference", "correct", 2.0) for i in range(7)]
    s.results += [
        CaseResult("m", "fact", "missed", 2.0),
        CaseResult("w", "fact", "wrong_type", 2.0),
    ]
    s.results += [CaseResult("x", "playbook", "missed", 2.0)]
    s.results += [
        CaseResult("n1", "none", "quiet", 1.0),
        CaseResult("n2", "none", "false_save", 1.0),
    ]
    d = s.as_dict()
    assert (d["correct"], d["expected_saves"], d["recall"]) == (7, 10, 0.7)
    assert (d["missed"], d["wrong_type"], d["false_saves"], d["negatives"]) == (
        2,
        1,
        1,
        2,
    )
    assert d["verdict"] == "FAIL"  # one false save fails the provider
    s.results = [r for r in s.results if r.outcome != "false_save"]
    assert s.verdict == "PASS"
    s.results.append(CaseResult("e", "fact", "error"))
    assert s.verdict == "INCOMPLETE"


def test_extract_writes():
    body = {
        "data": {
            "agent_step_response": [
                {
                    "Call": {
                        "tool_call": {
                            "function": {"name": "kubectl", "arguments": "{}"}
                        }
                    }
                },
                {
                    "Call": {
                        "tool_call": {
                            "function": {
                                "name": "memory_write",
                                "arguments": '{"type": "fact"}',
                            }
                        }
                    },
                    "Response": {"tool_response": {"content": 'Remembered: fact "x"'}},
                },
                {
                    "Call": {
                        "tool_call": {
                            "function": {"name": "memory_write", "arguments": "{bad"}
                        }
                    },
                    "Response": {
                        "tool_response": {"content": "memory_write: evidence must ..."}
                    },
                },
            ]
        }
    }
    writes = extract_writes(body)
    assert [(w.args, w.accepted) for w in writes] == [
        ({"type": "fact"}, True),
        ({}, False),
    ]


def test_parse_providers():
    assert parse_providers("a=db:1, b=db:2") == [("a", "db:1"), ("b", "db:2")]
    with pytest.raises(ValueError):
        parse_providers("a")


def test_cases_file_is_well_formed():
    from memory_golden.run import CASES_PATH as path

    cases = json.loads(path.read_text())
    assert len({c["id"] for c in cases}) == len(cases)
    for c in cases:
        assert c["expect"] in {"preference", "fact", "playbook", "none"}
        if c["expect"] != "none":
            assert c.get("must_contain") or c.get("must_contain_any"), c["id"]


def test_none_values_do_not_match():
    case = {"id": "n", "expect": "fact", "must_contain": ["none"]}
    w = Write(
        {"type": "fact", "subject": "x", "body": None, "resource_ids": [None]}, True
    )
    assert score_case(case, [w]) == "wrong_content"


def test_extract_writes_tolerates_odd_bodies():
    assert extract_writes({"data": None}) == []
    assert extract_writes({"data": "oops"}) == []


def test_extract_writes_skips_non_dict_steps():
    assert extract_writes({"data": {"agent_step_response": ["x", None]}}) == []


def test_extract_writes_non_object_arguments():
    step = {
        "Call": {
            "tool_call": {"function": {"name": "memory_write", "arguments": "[1]"}}
        },
        "Response": {"tool_response": {"content": "Remembered"}},
    }
    assert [
        w.args for w in extract_writes({"data": {"agent_step_response": [step]}})
    ] == [{}]


def test_extract_writes_dict_arguments():
    step = {
        "Call": {
            "tool_call": {
                "function": {"name": "memory_write", "arguments": {"type": "fact"}}
            }
        },
        "Response": {"tool_response": {"content": "Remembered"}},
    }
    writes = extract_writes({"data": {"agent_step_response": [step]}})
    assert [w.args for w in writes] == [{"type": "fact"}]


def test_extract_writes_non_dict_call_and_response():
    steps = [{"Call": "x"}, {"Call": {"tool_call": None}}, {"Response": []}]
    assert extract_writes({"data": {"agent_step_response": steps}}) == []


def test_extract_writes_non_list_steps():
    assert extract_writes({"data": {"agent_step_response": 7}}) == []


def test_refused_save_is_counted_but_not_a_false_save():
    s = ProviderSummary("p", source="db:1")
    s.results = [CaseResult("a", "preference", "correct")]
    s.results.append(CaseResult("n", "none", "refused_save"))
    d = s.as_dict()
    assert (d["refused_saves"], d["false_saves"], d["verdict"]) == (1, 0, "PASS")
    assert d["llm_config_source"] == "db:1"


def test_render_repeats_reports_worst_and_mean():
    runs = []
    for i, outcome in enumerate(["correct", "missed"], start=1):
        s = ProviderSummary("p", run=i)
        s.results = [CaseResult("a", "fact", outcome)]
        runs.append(s)
    row = render_repeats(runs).splitlines()[-1]
    assert row == "| p | 2 | PASS FAIL | 0% | 50% | 0 |"


def test_name_matches_whole_words_only():
    case = {"id": "f", "expect": "fact", "must_contain": ["staging"]}
    assert name_matches(case, "staging_cluster_schedule")
    assert not name_matches(case, "prestaging_jobs")
    utc = {"id": "u", "expect": "preference", "must_contain": ["utc"]}
    assert not name_matches(utc, "execute_order")


def test_wrong_type_with_wrong_content_still_fails():
    w = Write({"type": "fact", "subject": "staging", "body": "x"}, accepted=True)
    assert score_case(PREF, [w]) == "wrong_type"


def test_type_mixup_counts_toward_recall():
    s = ProviderSummary("p")
    s.results = [
        CaseResult("a", "fact", "type_mixup"),
        CaseResult("b", "fact", "correct"),
        CaseResult("c", "fact", "wrong_type"),
    ]
    d = s.as_dict()
    assert (d["correct"], d["expected_saves"], d["type_mixups"], d["wrong_type"]) == (
        2,
        3,
        1,
        1,
    )
