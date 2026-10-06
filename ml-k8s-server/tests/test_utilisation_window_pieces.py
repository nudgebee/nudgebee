"""A multi-day utilisation window the metrics store rejects is re-asked a day at a time.

The anomaly detector reads 7 or 14 days at a 2m step in one call. Prometheus answers
`query processing would load too many samples into memory` for a workload with enough
HTTP series, api-server passes that back as the key's error, and until now the key was
simply absent -- so the error-rate check for that workload got no data every hour (#39744).
"""

from datetime import datetime, timedelta, timezone
from unittest.mock import patch

import pytest

from server.metrics.prometheus_metrics import _entries_for

START = datetime(2026, 10, 1, 9, 0, 0, tzinfo=timezone.utc)
STEP_SECONDS = 120
TOO_MANY = "query processing would load too many samples into memory in query execution"


class _Column:
    utilisation_key = "workload_http_request_count"
    namespace = "prod"
    deployment_name = "api"


def _grid(start: datetime, end: datetime):
    """The points a Prometheus range query returns for [start, end] at STEP_SECONDS: both ends inclusive."""
    first, last = int(start.timestamp()), int(end.timestamp())
    return list(range(first, last + 1, STEP_SECONDS))


def _store(reject_whole_window=True, reject_pieces_from=None, error=TOO_MANY):
    """A fake transport: rejects the whole window, answers each day piece on the step grid."""
    calls = []

    def fake(**kwargs):
        start, end, on_key_error = kwargs["start_time"], kwargs["end_time"], kwargs.get("on_key_error")
        calls.append((start, end))
        key = kwargs["request"]["metrics"][0]
        window = (end - start).total_seconds()
        if (reject_whole_window and window > 86400) or (
            reject_pieces_from is not None and len(calls) - 1 >= reject_pieces_from
        ):
            on_key_error(key, error)
            return {}
        timestamps = _grid(start, end)
        return {key: [{"metric": {}, "timestamps": timestamps, "values": [str(t) for t in timestamps]}]}

    return fake, calls


def _fetch(days, fake, step="2m"):
    with patch("server.metrics.prometheus_metrics.query_utilisation_via_api_server", side_effect=fake):
        return _entries_for(
            query_obj=_Column(),
            query="workload_http_request_count",
            account_id="acct-1",
            tenant_id="tenant-1",
            start_time=START,
            end_time=START + timedelta(days=days),
            step=step,
            instant=False,
        )


def test_a_rejected_week_is_answered_in_seven_day_pieces_on_the_same_grid():
    fake, calls = _store()
    result = _fetch(7, fake)

    # One rejected call for the whole window, then one per day.
    assert len(calls) == 8
    assert calls[0] == (START, START + timedelta(days=7))
    assert calls[1:] == [(START + timedelta(days=d), START + timedelta(days=d + 1)) for d in range(7)]

    # Exactly the points the single query would have returned, each once, in order.
    assert len(result) == 1
    assert result[0]["timestamps"] == _grid(START, START + timedelta(days=7))
    assert result[0]["values"] == [str(t) for t in result[0]["timestamps"]]


def test_the_last_piece_ends_at_the_window_end_not_a_day_later():
    fake, calls = _store()
    result = _fetch(1.5, fake)

    assert calls[1:] == [
        (START, START + timedelta(days=1)),
        (START + timedelta(days=1), START + timedelta(days=1.5)),
    ]
    assert result[0]["timestamps"] == _grid(START, START + timedelta(days=1.5))


@pytest.mark.parametrize(
    "error",
    [
        TOO_MANY,
        "cannot select more than -search.maxSamplesPerQuery=50000000 samples",
        "exceeds -search.maxPointsPerTimeseries=30000; try increasing it",
    ],
)
def test_every_store_spelling_of_too_large_is_split(error):
    fake, calls = _store(error=error)
    assert _fetch(7, fake)
    assert len(calls) == 8


def test_any_other_error_is_not_retried():
    fake, calls = _store(error="parse error: unexpected character")
    assert _fetch(7, fake) == []
    assert len(calls) == 1


def test_a_window_of_one_day_or_less_is_not_split():
    # Nothing smaller to ask for; the key stays absent as before.
    fake, calls = _store(reject_whole_window=False, reject_pieces_from=0)
    assert _fetch(1, fake) == []
    assert len(calls) == 1


def test_a_window_without_an_explicit_step_is_not_split():
    # The backend sizes the step to the range, so day pieces would not share the week's grid.
    fake, calls = _store()
    assert _fetch(7, fake, step="") == []
    assert len(calls) == 1


def test_a_rejected_piece_gives_up_on_the_key_rather_than_returning_a_gap():
    # Days 1-3 answer, day 4 is rejected: no partial week, and no more calls.
    fake, calls = _store(reject_pieces_from=4)
    assert _fetch(7, fake) == []
    assert len(calls) == 5


def test_series_are_merged_by_their_labels():
    def fake(**kwargs):
        start, end, on_key_error = kwargs["start_time"], kwargs["end_time"], kwargs["on_key_error"]
        if (end - start).total_seconds() > 86400:
            on_key_error("workload_http_request_count", TOO_MANY)
            return {}
        timestamps = _grid(start, end)
        return {
            "workload_http_request_count": [
                {"metric": {"pod": "a"}, "timestamps": timestamps, "values": ["1"] * len(timestamps)},
                {"metric": {"pod": "b"}, "timestamps": timestamps, "values": ["2"] * len(timestamps)},
            ]
        }

    result = _fetch(2, fake)
    assert [s["metric"] for s in result] == [{"pod": "a"}, {"pod": "b"}]
    assert all(s["timestamps"] == _grid(START, START + timedelta(days=2)) for s in result)
