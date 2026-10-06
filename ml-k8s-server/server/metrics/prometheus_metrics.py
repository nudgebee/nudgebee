import json
import logging
from datetime import UTC, datetime, timedelta, timezone
from typing import Any, Dict, List, Optional, Union, cast

import pandas as pd
import requests
from server.metrics.api_server_metrics import (
    _step_seconds,
    query_utilisation_via_api_server,
    AgentNotConnectedError,
    query_metrics_via_api_server,
)
from server.metrics.metrics import Metrics
from server.metrics.prometheus_queries import (
    CpuUsage,
    MemUsage,
    MetricsInput,
    ReplicasCount,
    RequestCount,
    RequestDuration,
)
from server.utils.utils import (
    get_http_session_with_retry,
    get_prediction_duration,
    get_prediction_time_range,
    get_trace,
    resolve_tenant_id,
)

from opentelemetry import context
from opentelemetry.propagate import inject

logger = logging.getLogger(__name__)


def operate_data(data: List[MetricsInput], end_time: datetime, duration_in_days: int):
    times: Dict[int, Any] = {int((end_time - timedelta(hours=i)).timestamp()): 0 for i in range(duration_in_days * 24)}
    for dat in data:
        timestamps: List[int] = list(map(int, dat.timestamps))
        values = dat.values
        for _d in [(timestamps[i], values[i]) for i in range(min(len(timestamps), len(values)))]:
            times[_d[0]] = _d[1]
    return [[i, times[i]] for i in times]


# flake8: noqa: C901
def fetch_metrics(
    queries_obj: list,
    start_time: datetime,
    end_time: datetime,
    duration: Optional[int],
    step: str,
    account_id: str,
    instant: bool = False,
    raw: bool = False,
    tenant_id: str = "",
) -> list:
    with get_trace(__name__).start_as_current_span("fetch_metrics"):
        try:
            results = []
            start_time = start_time.replace(tzinfo=timezone.utc)
            end_time = end_time.replace(tzinfo=timezone.utc)

            effective_tenant = tenant_id or resolve_tenant_id(account_id)

            for query_obj in queries_obj:
                query = _query_label(query_obj)
                # Reduce log verbosity for metrics queries
                # logger.debug(f"fetching metrics for query : {query}")
                try:
                    entries = _entries_for(
                        query_obj=query_obj,
                        query=query,
                        account_id=str(account_id),
                        tenant_id=effective_tenant,
                        start_time=start_time,
                        end_time=end_time,
                        step=step,
                        instant=instant,
                    )
                except AgentNotConnectedError:
                    # Only the eBPF columns need the agent. Losing them is a column of
                    # zeros on the shared hourly grid, not a reason to drop the columns
                    # the account's own provider just answered.
                    logger.warning(
                        f"Agent not connected for account {account_id}; "
                        f"leaving this column empty and keeping the rest."
                    )
                    entries = []

                if raw:
                    return entries

                # An instant query deliberately keeps the raw entries: that is what this
                # function has always handed to the duration logic.
                data_list: List[Any] = list(entries) if instant else [MetricsInput(**i) for i in entries]

                if duration is None:
                    results.extend(data_list)
                else:
                    if isinstance(query_obj, str):
                        r_list = operate_data(data=data_list, end_time=end_time, duration_in_days=duration)
                    else:
                        r_list = query_obj.get_data(data=data_list, end_time=end_time, duration_in_days=duration)
                    logger.debug(f"length of r_list query : {query} : {len(r_list)}")
                    results.append(r_list)
        except Exception as e:
            if raw:
                return []
            msg = f"Fetch metrics failed :{e}"
            logger.error(msg)
            raise ValueError(msg)
        if duration is not None:
            return merge_lists(results)
        else:
            return results


def _query_label(query_obj: Any) -> str:
    """What to send, or — for a column answered by key — what to call it in a log.

    A keyed column has no PromQL of its own any more: api-server builds the native
    query from the key name. Returning the key keeps the log lines identifying which
    column is being fetched, and _entries_for ignores this value for such a column.
    """
    if isinstance(query_obj, str):
        return query_obj
    key = getattr(query_obj, "utilisation_key", None)
    if key:
        return str(key)
    return str(query_obj.get_query())


def _entries_for(
    query_obj: Any,
    query: str,
    account_id: str,
    tenant_id: str,
    start_time: datetime,
    end_time: datetime,
    step: Union[str, int, None],
    instant: bool,
) -> List[dict]:
    """Answer one column, from whichever store can answer it.

    A column that names a utilisation key is asked for BY NAME and the account's
    default provider answers it. A column that does not carries its own PromQL and
    goes to Prometheus, because it reads an eBPF metric that exists nowhere else.

    Both come back in the same wire shape, so the callers that build MetricsInput and
    the hourly grid below do not know which happened.
    """
    key = getattr(query_obj, "utilisation_key", None)
    if not key or instant:
        # instant stays on the PromQL path: it is used for one-off duration probes
        # that pass a raw query string, not one of these columns.
        return query_metrics_via_api_server(
            account_id=account_id,
            tenant_id=tenant_id,
            query=query,
            start_time=start_time,
            end_time=end_time,
            step=step,
            instant=instant,
        )

    request = {
        "kind": "deployment",
        "workload_namespace": query_obj.namespace,
        "workload_name": query_obj.deployment_name,
        "metrics": [key],
    }
    key_errors: Dict[str, str] = {}
    answered = query_utilisation_via_api_server(
        account_id=account_id,
        tenant_id=tenant_id,
        request=request,
        start_time=start_time,
        end_time=end_time,
        step=step,
        on_key_error=key_errors.__setitem__,
    )
    series = answered.get(key) or []
    if not series and _window_too_large(key_errors.get(key)):
        series = _fetch_in_day_pieces(key, request, account_id, tenant_id, start_time, end_time, step)
    scale = getattr(query_obj, "utilisation_scale", 1.0)
    if scale == 1.0:
        return series
    return [{**s, "values": [_scaled(v, scale) for v in (s.get("values") or [])]} for s in series]


# How Prometheus (`query processing would load too many samples into memory`) and
# VictoriaMetrics (`-search.maxSamplesPerQuery`, `-search.maxPointsPerTimeseries`) say a
# range query matched more than they will hold. The anomaly detector reads 7 or 14 days
# at a 2m step in one call, so a workload with enough HTTP series trips this every hour
# and, with the key absent, trains on nothing (#39744).
_WINDOW_TOO_LARGE_MARKERS = ("too many samples", "too many points", "maxsamplesperquery", "maxpointspertimeseries")
_PIECE_SECONDS = 86400


def _window_too_large(error: Optional[str]) -> bool:
    text = (error or "").lower()
    return any(marker in text for marker in _WINDOW_TOO_LARGE_MARKERS)


def _fetch_in_day_pieces(
    key: str,
    request: Dict[str, Any],
    account_id: str,
    tenant_id: str,
    start_time: datetime,
    end_time: datetime,
    step: Union[str, int, None],
) -> List[dict]:
    """Re-ask a rejected multi-day window one day at a time, on the same step grid.

    Each piece starts where the last one ended, a whole number of steps from the original
    start, so every point lands where the single query would have put it; the point the
    two pieces share is kept once. A piece that is itself rejected gives up on the whole
    key rather than returning the days that did answer: a training window with a day
    missing from the middle reads as a workload that was idle that day.
    """
    # Without an explicit step the backend picks one from the range, so a day piece
    # would come back at a different resolution from the week: nothing to stitch.
    step_seconds = _step_seconds(step)
    piece = 0 if step_seconds <= 0 else (_PIECE_SECONDS // step_seconds) * step_seconds
    if piece <= 0 or piece >= (end_time - start_time).total_seconds():
        logger.warning(f"utilisation key '{key}' was rejected as too large and the window cannot be split")
        return []

    merged: Dict[str, dict] = {}
    pieces = 0
    piece_start = start_time
    while piece_start < end_time:
        piece_end = min(piece_start + timedelta(seconds=piece), end_time)
        piece_errors: Dict[str, str] = {}
        answered = query_utilisation_via_api_server(
            account_id=account_id,
            tenant_id=tenant_id,
            request=request,
            start_time=piece_start,
            end_time=piece_end,
            step=step,
            on_key_error=piece_errors.__setitem__,
        )
        if key in piece_errors:
            logger.warning(
                f"utilisation key '{key}' failed for {piece_start:%Y-%m-%d %H:%M}..{piece_end:%Y-%m-%d %H:%M} "
                f"even as a day piece: {piece_errors[key]}"
            )
            return []
        for series in answered.get(key) or []:
            _merge_series(merged, series)
        pieces += 1
        piece_start = piece_end

    logger.info(f"utilisation key '{key}' answered in {pieces} day pieces after the whole window was rejected")
    return list(merged.values())


def _merge_series(merged: Dict[str, dict], series: dict) -> None:
    """Append a piece's samples to the series with the same labels, skipping any timestamp it already holds."""
    label_key = json.dumps(series.get("metric") or {}, sort_keys=True)
    target = merged.setdefault(label_key, {"metric": series.get("metric") or {}, "timestamps": [], "values": []})
    seen = set(target["timestamps"])
    for timestamp, value in zip(series.get("timestamps") or [], series.get("values") or []):
        if timestamp in seen:
            continue
        seen.add(timestamp)
        target["timestamps"].append(timestamp)
        target["values"].append(value)


def _scaled(value: Any, scale: float) -> Any:
    """Convert one sample into the column's unit, leaving an unreadable one alone.

    The wire carries Prometheus-formatted strings, so this parses and re-renders. A
    value that will not parse is passed through rather than dropped: the hourly grid
    treats whatever arrives as that bucket's reading, and inventing a zero there would
    read as a measured idle hour.
    """
    try:
        return repr(float(value) * scale)
    except (TypeError, ValueError):
        return value


def prometheus_range_query(
    promql_query: str,
    account_id: str,
    duration_minutes: Optional[int] = None,
    start_time: Optional[str] = None,
    end_time: Optional[str] = None,
    step: str = "1m",
    tenant_id: str = "",
) -> List[dict]:
    """Execute a Prometheus range query (time series data over a period).

    start_time/end_time are "YYYY-MM-DD HH:MM:SS UTC" strings, or pass duration_minutes
    for a window ending now. Returns [] rather than raising on a query failure -- that
    was already this helper's contract, and the volume-rightsizing caller relies on it.
    """
    try:
        resolved_end = _parse_time(end_time) or datetime.now(UTC)
        resolved_start = _parse_time(start_time)
        if resolved_start is None:
            resolved_start = resolved_end - timedelta(minutes=duration_minutes or 1)
        return query_metrics_via_api_server(
            account_id=str(account_id),
            tenant_id=tenant_id or resolve_tenant_id(account_id),
            query=promql_query,
            start_time=resolved_start,
            end_time=resolved_end,
            step=step,
            instant=False,
        )
    except Exception as e:
        logger.error(f"Failed to fetch metrics for range query: {promql_query[:100]}..., Error: {e}")
        return []


def _parse_time(value: Optional[str]) -> Optional[datetime]:
    try:
        return datetime.strptime(str(value), "%Y-%m-%d %H:%M:%S UTC").replace(tzinfo=timezone.utc)
    except (TypeError, ValueError):
        return None


def merge_lists(*lists) -> list:
    with get_trace(__name__).start_as_current_span("merge_lists"):
        outer_lists = [items for lst in lists for items in lst]
        dicts = [{item[0]: item[1] for item in outer_list} for outer_list in outer_lists]
        all_keys = set(key for d in dicts for key in d)
        merged_dict = {key: [d.get(key, None) for d in dicts] for key in all_keys}
        merged_list = [[key] + values for key, values in merged_dict.items()]
        return merged_list


class Prometheus(Metrics):
    def __init__(
        self,
        deployment_name: Optional[str],
        namespace_name: Optional[str],
        account_id: str,
        tenant_id: str = "",
    ):
        self.duration = get_prediction_duration(account_id)
        self.time_range = get_prediction_time_range(account_id)
        self.namespace_name = namespace_name
        self.deployment_name = deployment_name
        self.account_id = account_id
        # Needed for the api-server transport, which scopes the query by tenant. Callers
        # that hold one pass it; the rest fall back to a lookup keyed on the account.
        self.tenant_id = tenant_id or resolve_tenant_id(account_id)

    def get_metrics(self) -> pd.DataFrame:
        with get_trace(__name__).start_as_current_span("get_metrics"):
            try:
                logger.debug(f"Fetching metrics with details : {self}")
                replicas_count_obj = ReplicasCount(namespace=self.namespace_name, deployment_name=self.deployment_name)
                request_count_obj = RequestCount(namespace=self.namespace_name, deployment_name=self.deployment_name)
                request_duration_obj = RequestDuration(
                    namespace=self.namespace_name, deployment_name=self.deployment_name
                )
                mem_usage_obj = MemUsage(namespace=self.namespace_name, deployment_name=self.deployment_name)
                cpu_usage_obj = CpuUsage(namespace=self.namespace_name, deployment_name=self.deployment_name)
                queries = [replicas_count_obj, request_count_obj, request_duration_obj, mem_usage_obj, cpu_usage_obj]

                end_time = datetime.now(UTC)
                start_time = end_time - timedelta(days=self.duration)

                results = fetch_metrics(
                    queries_obj=queries,
                    start_time=start_time,
                    end_time=end_time,
                    step=self.time_range,
                    account_id=self.account_id,
                    duration=self.duration,
                    tenant_id=self.tenant_id,
                )

                cols = ["timestamp", "replicas", "rps", "latency", "memory", "cpu"]
                df = pd.DataFrame(results, columns=cols)
                df.sort_values(by="timestamp", inplace=True)
                # df.to_csv("output_input.csv", index=False, encoding="utf-8")
                logger.info(f"length of df: {len(df)}")
                return df
            except Exception as e:
                msg = f"Failed to fetch metrics: {e}"
                logger.error(msg)
                raise ValueError(msg)

    def query_metrics(
        self,
        query: str,
        start_time: datetime,
        end_time: datetime,
        step: str = "",
        instant: bool = False,
        raw: bool = False,
    ) -> list:
        results = fetch_metrics(
            queries_obj=[query],
            start_time=start_time,
            end_time=end_time,
            step=step,
            account_id=self.account_id,
            duration=self.duration,
            instant=instant,
            raw=raw,
            tenant_id=self.tenant_id,
        )
        return results

    def check_prometheus_connection(self):
        # do nothing
        pass

    def custom_query(self, query: str, start_time: Optional[datetime] = None, end_time: Optional[datetime] = None):
        if end_time is None:
            end_time = datetime.now(UTC)
        if start_time is None:
            start_time = datetime.now(UTC) - timedelta(days=14)
        response = self.query_metrics(query, start_time, end_time, "", True, raw=True)
        if len(response) > 0:
            return [
                {
                    "metric": r["metric"],
                    "value": [r["value"]["timestamp"], r["value"]["value"]],
                }
                for r in response
            ]
        return response

    def custom_query_range(self, query: str, start_time: datetime, end_time: datetime, step: str):
        response = self.query_metrics(query, start_time, end_time, step, raw=True)
        return response
