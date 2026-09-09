import threading
from unittest.mock import MagicMock, patch

import pytest

from notifications_server.configs import settings
from notifications_server.services import slack_progress


@pytest.fixture(autouse=True)
def reset_poller_counter():
    slack_progress._active_pollers = 0
    yield
    slack_progress._active_pollers = 0


def _entry(**overrides):
    entry = {
        "team_id": "T111",
        "channel_id": "C222",
        "slack_user_id": "U333",
        "session_id": "C222-1000.1",
        "account_id": "acc-1",
        "tenant_id": "tenant-1",
        "user_id": "user-1",
    }
    entry.update(overrides)
    return entry


def _tool_row(
    row_id,
    status,
    tool_name="get_pod_logs",
    thought="checking logs",
    updated_at="2026-08-14T10:00:00Z",
    parameters=None,
):
    return {
        "id": row_id,
        "status": status,
        "tool_name": tool_name,
        "thought": thought,
        "updated_at": updated_at,
        "parameters": parameters,
    }


class TestBuildChunks:
    def test_new_in_progress_tool_emits_task_titled_from_thought(self):
        sent = {}
        chunks = slack_progress._build_chunks([_tool_row("t1", "IN_PROGRESS")], sent)
        assert chunks == [
            {
                "type": "task_update",
                "id": "t1",
                "title": "Checking logs",
                "status": "in_progress",
            }
        ]
        assert sent == {"t1": "in_progress"}

    def test_falls_back_to_tool_name_when_thought_missing(self):
        sent = {}
        chunks = slack_progress._build_chunks([_tool_row("t1", "IN_PROGRESS", thought=None)], sent)
        assert chunks[0]["title"] == "Get pod logs"

    def test_uses_command_from_parameters_when_thought_missing(self):
        sent = {}
        rows = [
            _tool_row(
                "t1",
                "IN_PROGRESS",
                tool_name="cluster_command",
                thought="",
                parameters="kubectl get pods -n nudgebee -o wide",
            )
        ]
        chunks = slack_progress._build_chunks(rows, sent)
        assert chunks[0]["title"] == "kubectl get pods -n nudgebee -o wide"

    def test_relay_sub_steps_get_distinct_titles_from_their_commands(self):
        # The bug: five relay probes of one search all rendered as "Cluster command".
        sent = {}
        rows = [
            _tool_row(
                "s#1",
                "ERROR",
                tool_name="cluster_command",
                thought="",
                parameters="kubectl get pods pod-a -n ns-one",
                updated_at="2026-08-27T06:18:36Z",
            ),
            _tool_row(
                "s#2",
                "ERROR",
                tool_name="cluster_command",
                thought="",
                parameters="kubectl get pods pod-b -n ns-two",
                updated_at="2026-08-27T06:18:37Z",
            ),
        ]
        titles = [c["title"] for c in slack_progress._build_chunks(rows, sent)]
        assert titles == [
            "kubectl get pods pod-a -n ns-one",
            "kubectl get pods pod-b -n ns-two",
        ]

    def test_status_transitions_and_dedupe(self):
        sent = {}
        slack_progress._build_chunks([_tool_row("t1", "IN_PROGRESS")], sent)
        # Same status again: nothing new to send.
        assert slack_progress._build_chunks([_tool_row("t1", "IN_PROGRESS")], sent) == []
        # Terminal transition goes out once, then never repeats or downgrades.
        chunks = slack_progress._build_chunks([_tool_row("t1", "SUCCESS")], sent)
        assert [c["status"] for c in chunks] == ["complete"]
        assert slack_progress._build_chunks([_tool_row("t1", "SUCCESS")], sent) == []
        assert slack_progress._build_chunks([_tool_row("t1", "IN_PROGRESS")], sent) == []

    def test_status_mapping(self):
        sent = {}
        rows = [
            _tool_row("a", "SUCCESS"),
            _tool_row("b", "EMPTY_RESULT"),
            _tool_row("c", "ERROR"),
            _tool_row("d", "FAILURE"),
            _tool_row("e", "TERMINATED"),
            _tool_row("f", "WAITING"),
            _tool_row("g", "something_unknown"),
        ]
        statuses = {c["id"]: c["status"] for c in slack_progress._build_chunks(rows, sent)}
        assert statuses == {
            "a": "complete",
            "b": "complete",
            "c": "error",
            "d": "error",
            "e": "error",
            "f": "in_progress",
            "g": "in_progress",
        }

    def test_rows_sorted_by_updated_at(self):
        sent = {}
        rows = [
            _tool_row("later", "IN_PROGRESS", updated_at="2026-08-14T10:00:02Z"),
            _tool_row("earlier", "IN_PROGRESS", updated_at="2026-08-14T10:00:01Z"),
        ]
        chunks = slack_progress._build_chunks(rows, sent)
        assert [c["id"] for c in chunks] == ["earlier", "later"]

    def test_in_progress_title_only_hard_capped_not_cosmetically_truncated(self):
        sent = {}
        rows = [_tool_row("t1", "IN_PROGRESS", tool_name="x" * 500, thought="y" * 500)]
        chunks = slack_progress._build_chunks(rows, sent)
        assert len(chunks[0]["title"]) == slack_progress._TASK_FIELD_LIMIT
        assert not chunks[0]["title"].endswith("…")

    def test_settled_title_is_cosmetically_truncated(self):
        sent = {}
        rows = [_tool_row("t1", "SUCCESS", tool_name="x" * 500, thought="y" * 500)]
        chunks = slack_progress._build_chunks(rows, sent)
        assert len(chunks[0]["title"]) == slack_progress._TASK_TITLE_CHAR_LIMIT
        assert chunks[0]["title"].endswith("…")

    def test_rows_without_id_are_skipped(self):
        sent = {}
        chunks = slack_progress._build_chunks(
            [{"status": "IN_PROGRESS"}, _tool_row("t1", "IN_PROGRESS", thought="internal reasoning")], sent
        )
        assert len(chunks) == 1
        assert chunks[0]["title"] == "Internal reasoning"

    def test_none_tool_calls(self):
        assert slack_progress._build_chunks(None, {}) == []


class TestTaskTitle:
    def test_humanizes_snake_case_when_no_thought(self):
        assert slack_progress._task_title("get_pod_logs") == "Get pod logs"

    def test_empty_name_falls_back(self):
        assert slack_progress._task_title(None) == "Working"
        assert slack_progress._task_title("___") == "Working"

    def test_prefers_thought_over_tool_name(self):
        assert slack_progress._task_title("recommendation_execute", "scaling down idle EC2 instance") == (
            "Scaling down idle EC2 instance"
        )

    def test_blank_thought_falls_back_to_tool_name(self):
        assert slack_progress._task_title("get_pod_logs", "   ") == "Get pod logs"

    def test_thought_whitespace_collapsed(self):
        assert slack_progress._task_title("get_pod_logs", "checking\nlogs   for  crash") == "Checking logs for crash"

    def test_thought_truncated_with_ellipsis(self):
        title = slack_progress._task_title("get_pod_logs", "x" * 100)
        assert len(title) == slack_progress._TASK_TITLE_CHAR_LIMIT
        assert title.endswith("…")

    def test_thought_truncated_at_word_boundary(self):
        thought = "carefully checking the current health of the cluster before restarting the failing pods"
        full_title = " ".join(thought.split())
        full_title = full_title[:1].upper() + full_title[1:]

        title = slack_progress._task_title("get_pod_logs", thought)

        assert title.endswith("…")
        core = title[:-1].rstrip()
        assert core
        assert full_title.startswith(core)
        # The cut must land right after a full word, never mid-word.
        assert len(core) == len(full_title) or full_title[len(core)] == " "

    def test_in_progress_status_skips_cosmetic_truncation(self):
        title = slack_progress._task_title("get_pod_logs", "x" * 100, status="in_progress")
        assert len(title) == 100
        assert not title.endswith("…")

    def test_settled_status_still_truncates(self):
        title = slack_progress._task_title("get_pod_logs", "x" * 100, status="complete")
        assert len(title) == slack_progress._TASK_TITLE_CHAR_LIMIT
        assert title.endswith("…")

    @pytest.mark.parametrize(
        "thought, expected",
        [
            ("let's search the logs for errors", "Searching the logs for errors"),
            ("let me search the logs for errors", "Searching the logs for errors"),
            ("I will query the database for recent rows", "Querying the database for recent rows"),
            ("I'm going to query the database for recent rows", "Querying the database for recent rows"),
            ("I need to search for related events", "Searching for related events"),
            ("I have identified the root cause", "Identified the root cause"),
            ("I attempted to restart the pod", "Attempted to restart the pod"),
            # "The" is a genuine subject here, not deliberation narration - leave it.
            ("The user is asking for CPU metrics", "The user is asking for CPU metrics"),
        ],
    )
    def test_deliberation_prefix_rewritten_to_lead_with_action(self, thought, expected):
        assert slack_progress._task_title("get_pod_logs", thought) == expected

    def test_deliberation_prefix_gerund_handles_consonant_doubling(self):
        assert slack_progress._task_title("get_pod_logs", "let's run the diagnostic script") == (
            "Running the diagnostic script"
        )

    def test_deliberation_prefix_gerund_does_not_double_consonant_for_sync(self):
        assert slack_progress._task_title("get_pod_logs", "I'll sync the cluster state first") == (
            "Syncing the cluster state first"
        )

    def test_deliberation_prefix_handles_ive_contraction(self):
        assert slack_progress._task_title("get_pod_logs", "I've identified the root cause") == (
            "Identified the root cause"
        )

    def test_deliberation_prefix_handles_ill_contraction(self):
        assert slack_progress._task_title("get_pod_logs", "I'll query the database for recent rows") == (
            "Querying the database for recent rows"
        )

    def test_deliberation_prefix_handles_typographic_apostrophe(self):
        assert slack_progress._task_title("get_pod_logs", "I’ll query the database for recent rows") == (
            "Querying the database for recent rows"
        )

    @pytest.mark.parametrize(
        "thought",
        [
            "I am checking the node pressure conditions",
            "I can see the pod is OOMKilled",
            "I should verify the HPA settings",
        ],
    )
    def test_bare_i_present_tense_lead_in_is_not_stripped(self, thought):
        # Only past-tense openers ("I attempted") drop the subject cleanly -
        # present tense/modal openers ("I am/can/should") would read as
        # broken English with "I" removed, so those pass through unchanged.
        assert slack_progress._task_title("get_pod_logs", thought) == thought

    def test_and_replaced_with_ampersand(self):
        assert slack_progress._task_title("get_pod_logs", "checking pods and nodes") == "Checking pods & nodes"

    def test_and_replacement_does_not_touch_substrings(self):
        # "sandbox" and "android" contain "and" but aren't the word "and".
        assert slack_progress._task_title("get_pod_logs", "checking the sandbox and android agent") == (
            "Checking the sandbox & android agent"
        )

    def test_and_replaced_in_tool_name_fallback(self):
        assert slack_progress._task_title("check_pods_and_nodes") == "Check pods & nodes"

    def test_backticks_stripped_from_thought(self):
        assert (
            slack_progress._task_title("get_pod_logs", "resolve `app-dev` pods in `nudgebee` namespace using kubectl")
            == "Resolve app-dev pods in nudgebee namespace using kubectl"
        )

    def test_asterisks_stripped_from_thought(self):
        assert slack_progress._task_title("get_pod_logs", "checking the **error rate** metric") == (
            "Checking the error rate metric"
        )

    def test_emphasis_underscores_stripped_but_snake_case_identifiers_preserved(self):
        assert slack_progress._task_title("get_pod_logs", "running _diagnostics_ on cloud_command status") == (
            "Running diagnostics on cloud_command status"
        )

    def test_thought_of_only_wrapper_chars_falls_back_to_original(self):
        # Stripping markdown wrapper chars from a thought that is only those
        # chars would otherwise leave an empty title.
        assert slack_progress._task_title("get_pod_logs", "**") == "**"

    def test_thought_without_deliberation_prefix_is_unchanged(self):
        assert slack_progress._task_title("get_pod_logs", "correlating pod restarts with the deploy") == (
            "Correlating pod restarts with the deploy"
        )

    def test_parameters_command_used_when_no_thought(self):
        # Verbatim: not capitalised ("kubectl", not "Kubectl").
        assert (
            slack_progress._task_title(
                "cluster_command", thought="", parameters='{"command": "kubectl get pods -n nudgebee"}'
            )
            == "kubectl get pods -n nudgebee"
        )

    def test_parameters_ignored_when_thought_present(self):
        assert (
            slack_progress._task_title(
                "cluster_command", thought="checking pods", parameters='{"command": "kubectl get pods"}'
            )
            == "Checking pods"
        )

    def test_parameters_fall_through_to_tool_name_when_unreadable(self):
        assert slack_progress._task_title("cluster_command", thought="", parameters='{"limit": 500}') == (
            "Cluster command"
        )

    def test_parameters_command_skips_and_markdown_substitutions(self):
        # A real command must survive verbatim - no "and" -> "&", no stripping of
        # jsonpath brackets/asterisks.
        command = "kubectl get pods -o=jsonpath='{range .items[*]}{.metadata.name}{end}' and wait"
        assert slack_progress._task_title("cluster_command", thought="", parameters=command, status="in_progress") == (
            "kubectl get pods -o=jsonpath='{range .items[*]}{.metadata.name}{end}' and wait"
        )

    def test_command_title_is_capped_even_while_in_progress(self):
        # Unlike a thought, a long command is capped the moment it renders - a
        # 200-char kubectl/jsonpath line is noise to watch mid-run.
        command = "kubectl get pods " + "pod-name-that-is-fairly-long " * 6 + "-n nudgebee"
        title = slack_progress._task_title("cluster_command", thought="", parameters=command, status="in_progress")
        assert len(title) <= slack_progress._TASK_TITLE_CHAR_LIMIT
        assert title.endswith("…")

    def test_structured_param_branches_lead_with_tool_name(self):
        assert (
            slack_progress._task_title("rg", thought="", parameters='{"pattern": "WithTools", "path": "llm"}')
            == "rg WithTools"
        )
        assert (
            slack_progress._task_title(
                "file_view", thought="", parameters='{"file_path": "llm/x.go", "start_line": 10, "end_line": 20}'
            )
            == "file_view llm/x.go:10-20"
        )


class TestCommandFromParameters:
    def test_blank(self):
        assert slack_progress._command_from_parameters(None) == ""
        assert slack_progress._command_from_parameters("   ") == ""

    def test_bare_string_command_collapsed(self):
        assert slack_progress._command_from_parameters("recent events  in\npayments namespace") == (
            "recent events in payments namespace"
        )

    def test_bare_url_returned_as_is(self):
        url = "https://search.brave.com/search?q=LLM+gateway&safesearch=strict"
        assert slack_progress._command_from_parameters(url, "crawl_execute") == url

    def test_multiline_command_collapsed_to_one_line(self):
        assert slack_progress._command_from_parameters('{"command": "grep foo\\n  bar.txt"}') == "grep foo bar.txt"

    def test_json_command_key_not_prefixed_with_tool_name(self):
        assert slack_progress._command_from_parameters('{"command": "kubectl get ns"}', "cluster_command") == (
            "kubectl get ns"
        )

    def test_json_query_key(self):
        assert slack_progress._command_from_parameters('{"query": "SELECT 1;"}') == "SELECT 1;"

    def test_json_args_list_led_with_tool_name(self):
        assert slack_progress._command_from_parameters('{"args": ["log", "-n", "5"]}', "git") == "git log -n 5"

    def test_json_file_path_with_line_range_led_with_tool_name(self):
        assert (
            slack_progress._command_from_parameters(
                '{"file_path": "llm/x.go", "start_line": 10, "end_line": 20}', "file_view"
            )
            == "file_view llm/x.go:10-20"
        )

    def test_json_pattern_key_led_with_tool_name(self):
        assert slack_progress._command_from_parameters('{"pattern": "WithTools", "path": "llm/llm-server"}', "rg") == (
            "rg WithTools"
        )

    def test_lead_with_tool_noop_without_tool_name(self):
        assert slack_progress._command_from_parameters('{"args": ["log", "-n", "5"]}') == "log -n 5"

    def test_secret_only_blob_yields_nothing(self):
        assert slack_progress._command_from_parameters('{"github_token": "abc", "credentials": "xyz"}') == ""

    def test_malformed_json_yields_nothing(self):
        assert slack_progress._command_from_parameters('{"command": ') == ""

    def test_pre_parsed_dict_parameters(self):
        assert slack_progress._command_from_parameters({"command": "kubectl get ns"}, "cluster_command") == (
            "kubectl get ns"
        )

    def test_pre_parsed_list_parameters(self):
        assert slack_progress._command_from_parameters(["log", "-n", "5"], "git") == "git log -n 5"

    def test_pre_parsed_empty_container_yields_nothing(self):
        assert slack_progress._command_from_parameters({}, "git") == ""
        assert slack_progress._command_from_parameters([], "git") == ""


class TestStartProgressPoller:
    def test_missing_required_field_spawns_nothing(self):
        with patch.object(threading, "Thread") as thread_cls:
            slack_progress.start_progress_poller(MagicMock(), _entry(slack_user_id=None), "1000.1", "C222-1000.1")
        thread_cls.assert_not_called()

    def test_spawns_daemon_thread_with_payload_session_id(self):
        with patch.object(threading, "Thread") as thread_cls:
            slack_progress.start_progress_poller(MagicMock(), _entry(session_id="stale"), "1000.1", "event-abc")
        assert thread_cls.call_args.kwargs["daemon"] is True
        passed_entry = thread_cls.call_args.kwargs["args"][1]
        assert passed_entry["session_id"] == "event-abc"

    def test_poller_cap_blocks_new_pollers(self):
        with patch.object(settings.slack, "thinking_steps_max_pollers", 0):
            with patch.object(threading, "Thread") as thread_cls:
                slack_progress.start_progress_poller(MagicMock(), _entry(), "1000.1", "C222-1000.1")
        thread_cls.assert_not_called()

    def test_never_raises(self):
        with patch.object(threading, "Thread", side_effect=RuntimeError("boom")):
            slack_progress.start_progress_poller(MagicMock(), _entry(), "1000.1", "C222-1000.1")


class TestStopProgressStream:
    def test_no_stream_key_is_a_noop(self):
        common = MagicMock()
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache_cls.return_value.get_event_entry.return_value = {"channel_id": "C222"}
            slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        common.get_slack_installation.assert_not_called()

    def test_clears_key_before_stopping(self):
        common = MagicMock()
        common.get_slack_installation.return_value.token = "xoxb-test"
        calls = []
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache = cache_cls.return_value
            cache.get_event_entry.return_value = {"stream_ts": "2000.2"}
            cache.remove_event_keys.side_effect = lambda *a: calls.append("clear")
            common.slack_app.client.append_stream.side_effect = lambda **kw: calls.append("append")
            common.slack_app.client.stop_stream.side_effect = lambda **kw: calls.append("stop")
            slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        assert calls == ["clear", "append", "stop"]
        common.slack_app.client.stop_stream.assert_called_once_with(token="xoxb-test", channel_id="C222", ts="2000.2")
        # No progress_since on the entry: nothing to catch up on, but the
        # placeholder still gets relabeled back to complete on its own.
        appended_chunks = common.slack_app.client.append_stream.call_args.kwargs["chunks"]
        assert appended_chunks == [
            {
                "type": "task_update",
                "id": slack_progress._INITIAL_TASK_ID,
                "title": slack_progress._INITIAL_TASK_TITLE,
                "status": "complete",
            }
        ]

    def test_flushes_final_delta_before_stopping(self):
        common = MagicMock()
        common.get_slack_installation.return_value.token = "xoxb-test"
        calls = []
        delta = {"tool_calls": [_tool_row("t1", "SUCCESS")]}
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache = cache_cls.return_value
            cache.get_event_entry.return_value = {"stream_ts": "2000.2", "progress_since": "2026-08-14T10:00:00Z"}
            with patch.object(slack_progress, "_fetch_delta", return_value=delta):
                common.slack_app.client.append_stream.side_effect = lambda **kw: calls.append(("append", kw))
                common.slack_app.client.stop_stream.side_effect = lambda **kw: calls.append(("stop", kw))
                slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        assert [c[0] for c in calls] == ["append", "stop"]
        appended_chunks = calls[0][1]["chunks"]
        assert appended_chunks == [
            {"type": "task_update", "id": "t1", "title": "Checking logs", "status": "complete"},
            {
                "type": "task_update",
                "id": slack_progress._INITIAL_TASK_ID,
                "title": slack_progress._INITIAL_TASK_TITLE,
                "status": "complete",
            },
        ]
        assert calls[0][1]["ts"] == "2000.2"

    def test_flush_truncates_a_row_still_in_progress_that_gets_force_settled(self):
        # A row that's still WAITING (in_progress) in the DB when the final
        # catch-up flush runs gets force-settled to "complete" - the title it
        # ships with must match that final status (truncated), not the
        # full-text in_progress rendering it would otherwise get, since this
        # is the last update the row will ever receive.
        common = MagicMock()
        common.get_slack_installation.return_value.token = "xoxb-test"
        calls = []
        delta = {"tool_calls": [_tool_row("t1", "WAITING", thought="y" * 100)]}
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache = cache_cls.return_value
            cache.get_event_entry.return_value = {"stream_ts": "2000.2", "progress_since": "2026-08-14T10:00:00Z"}
            with patch.object(slack_progress, "_fetch_delta", return_value=delta):
                common.slack_app.client.append_stream.side_effect = lambda **kw: calls.append(("append", kw))
                common.slack_app.client.stop_stream.side_effect = lambda **kw: calls.append(("stop", kw))
                slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        task_chunk = calls[0][1]["chunks"][0]
        assert task_chunk["status"] == "complete"
        assert task_chunk["title"].endswith("…")
        assert len(task_chunk["title"]) == slack_progress._TASK_TITLE_CHAR_LIMIT

    def test_flush_failure_still_relabels_placeholder_and_stops_the_stream(self):
        # Reproduces a live incident (2026-08-19): the catch-up fetch failing
        # (timeout, or any other exception) must not skip relabeling the
        # synthetic placeholder task back to complete - it had been stuck
        # permanently showing the mid-run "investigating..." title because
        # the whole flush bailed out before reaching that relabel.
        common = MagicMock()
        common.get_slack_installation.return_value.token = "xoxb-test"
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache = cache_cls.return_value
            cache.get_event_entry.return_value = {"stream_ts": "2000.2", "progress_since": "2026-08-14T10:00:00Z"}
            with patch.object(slack_progress, "_fetch_delta", side_effect=RuntimeError("boom")):
                slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        common.slack_app.client.stop_stream.assert_called_once_with(token="xoxb-test", channel_id="C222", ts="2000.2")
        common.slack_app.client.append_stream.assert_called_once_with(
            token="xoxb-test",
            channel_id="C222",
            ts="2000.2",
            chunks=[
                {
                    "type": "task_update",
                    "id": slack_progress._INITIAL_TASK_ID,
                    "title": slack_progress._INITIAL_TASK_TITLE,
                    "status": "complete",
                }
            ],
        )

    def test_flush_timeout_still_relabels_placeholder(self):
        # Same incident, but via the realistic path: _fetch_delta's own
        # internal error handling converts a timeout into a None return
        # rather than raising.
        common = MagicMock()
        common.get_slack_installation.return_value.token = "xoxb-test"
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache = cache_cls.return_value
            cache.get_event_entry.return_value = {"stream_ts": "2000.2", "progress_since": "2026-08-14T10:00:00Z"}
            with patch.object(slack_progress, "_fetch_delta", return_value=None):
                slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        common.slack_app.client.append_stream.assert_called_once_with(
            token="xoxb-test",
            channel_id="C222",
            ts="2000.2",
            chunks=[
                {
                    "type": "task_update",
                    "id": slack_progress._INITIAL_TASK_ID,
                    "title": slack_progress._INITIAL_TASK_TITLE,
                    "status": "complete",
                }
            ],
        )
        common.slack_app.client.stop_stream.assert_called_once()

    def test_falls_back_to_passed_entry_and_never_raises(self):
        common = MagicMock()
        common.slack_app.client.stop_stream.side_effect = RuntimeError("already stopped")
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache_cls.return_value.get_event_entry.return_value = None
            slack_progress.stop_progress_stream(common, {"stream_ts": "2000.2"}, "C222", "T111", "1000.1")
        common.slack_app.client.stop_stream.assert_called_once()

    def test_pending_claim_clears_key_without_slack_call(self):
        common = MagicMock()
        with patch.object(slack_progress, "Cache") as cache_cls:
            cache = cache_cls.return_value
            cache.get_event_entry.return_value = {"stream_ts": "pending-fixed"}
            slack_progress.stop_progress_stream(common, None, "C222", "T111", "1000.1")
        cache.remove_event_keys.assert_called_once_with("1000.1", ["stream_ts", "progress_since"])
        common.slack_app.client.stop_stream.assert_not_called()


class TestPollLifecycle:
    def _common(self):
        common = MagicMock()
        common.get_slack_installation.return_value.token = "xoxb-test"
        common.slack_app.client.start_stream.return_value = {"ts": "3000.3"}
        return common

    def _run(self, common, cache, deltas=()):
        """Run _poll with instant sleeps, a fixed claim token, and canned deltas."""
        fetches = iter(deltas)
        with patch.object(slack_progress, "Cache", return_value=cache):
            with patch.object(slack_progress.time, "sleep"):
                with patch.object(slack_progress.uuid, "uuid4", return_value=MagicMock(hex="fixed")):
                    with patch.object(slack_progress, "_fetch_delta", side_effect=lambda *a: next(fetches, None)):
                        slack_progress._poll(common, _entry(), "1000.1")

    # _poll reads the cache entry: before starting (leftover check), after
    # startStream (claim re-check), once per loop iteration, and in finally.

    def test_superseded_stream_is_stopped_before_starting_a_new_one(self):
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{"stream_ts": "old.1"}]
        cache.update_event_entry.return_value = False
        self._run(common, cache)
        stopped = [c.kwargs["ts"] for c in common.slack_app.client.stop_stream.call_args_list]
        assert stopped == ["old.1"]
        common.slack_app.client.start_stream.assert_not_called()

    def test_exits_without_stopping_when_settle_took_the_key(self):
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [
            {},
            {"stream_ts": "pending-fixed"},
            {"stream_ts": "different"},
            {"stream_ts": "different"},
        ]
        cache.update_event_entry.return_value = True
        self._run(common, cache)
        common.slack_app.client.stop_stream.assert_not_called()

    def test_settle_racing_startstream_still_stops_the_stream(self):
        common = self._common()
        cache = MagicMock()
        # The pending claim is gone by the post-start re-check: the settle
        # handler cleared it while chat.startStream was in flight.
        cache.get_event_entry.side_effect = [{}, None]
        cache.update_event_entry.return_value = True
        self._run(common, cache)
        common.slack_app.client.stop_stream.assert_called_once()
        assert common.slack_app.client.stop_stream.call_args.kwargs["ts"] == "3000.3"

    def test_expired_entry_mid_run_still_stops_the_stream(self):
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}, None, None]
        cache.update_event_entry.return_value = True
        self._run(common, cache)
        common.slack_app.client.stop_stream.assert_called_once()
        assert common.slack_app.client.stop_stream.call_args.kwargs["ts"] == "3000.3"

    def test_terminal_conversation_stops_own_stream_when_settle_never_came(self):
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [
            {},
            {"stream_ts": "pending-fixed"},
            {"stream_ts": "3000.3"},
            {"stream_ts": "3000.3"},
        ]
        cache.update_event_entry.return_value = True
        # A zero-tool-call turn (e.g. "hi") still has to show up in `messages`
        # for the terminal status to be trusted — an empty delta alongside
        # COMPLETED would now be treated as a stale read from a prior turn.
        # The message needs a real response body: a bare row with no response
        # is what llm-server's ack-only write looks like (see
        # test_ack_only_message_does_not_count_as_turn_activity) and must not
        # count on its own.
        delta = {
            "conversation": {"status": "COMPLETED"},
            "messages": [{"id": "m1", "response": "Hello!"}],
            "tool_calls": [],
            "cursor": "c1",
        }
        # Same delta twice: the main loop's poll, then the finally block's
        # own re-fetch from turn start once it decides to close the panel.
        self._run(common, cache, deltas=[delta, delta])
        common.slack_app.client.stop_stream.assert_called_once()
        cache.remove_event_keys.assert_called_with("1000.1", ["stream_ts", "progress_since"])
        start_chunks = common.slack_app.client.start_stream.call_args.kwargs["chunks"]
        assert start_chunks == [
            {"type": "plan_update", "title": "Thinking"},
            {
                "type": "task_update",
                "id": slack_progress._INITIAL_TASK_ID,
                "title": slack_progress._INITIAL_TASK_TITLE,
                "status": "in_progress",
            },
        ]
        # No tool calls, so the main loop's own poll has nothing to append —
        # the synthetic task stays in_progress instead of completing early.
        # Only the finally block's flush, right before the panel closes,
        # resolves it to complete.
        appended = common.slack_app.client.append_stream.call_args_list
        assert len(appended) == 1
        assert appended[0].kwargs["chunks"] == [
            {
                "type": "task_update",
                "id": slack_progress._INITIAL_TASK_ID,
                "title": slack_progress._INITIAL_TASK_TITLE,
                "status": "complete",
            }
        ]

    def test_appends_tool_chunks_from_delta(self):
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}] + [{"stream_ts": "3000.3"}] * 3
        cache.update_event_entry.return_value = True
        deltas = [
            {"conversation": {"status": "IN_PROGRESS"}, "tool_calls": [_tool_row("t1", "IN_PROGRESS")], "cursor": "c1"},
            {"conversation": {"status": "COMPLETED"}, "tool_calls": [_tool_row("t1", "SUCCESS")], "cursor": "c2"},
        ]
        self._run(common, cache, deltas=deltas)
        appended = common.slack_app.client.append_stream.call_args_list
        # The main loop's two tool-status sends, plus the finally block's
        # guaranteed placeholder finalize once its own catch-up fetch (the
        # deltas iterator is exhausted by then) returns nothing new.
        assert [c.kwargs["chunks"][0]["status"] for c in appended] == ["in_progress", "complete", "complete"]
        assert appended[-1].kwargs["chunks"][0]["id"] == slack_progress._INITIAL_TASK_ID
        assert all(c.kwargs["ts"] == "3000.3" for c in appended)

    def test_synthetic_task_reopens_in_gaps_between_real_tools(self):
        """Between t1 finishing and t2 starting, nothing real is in_progress —
        the synthetic task must reopen (relabeled, since real activity has
        started) rather than leaving the panel with everything checked off."""
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}] + [{"stream_ts": "3000.3"}] * 5
        cache.update_event_entry.return_value = True
        t1_running = {
            "conversation": {"status": "IN_PROGRESS"},
            "tool_calls": [_tool_row("t1", "IN_PROGRESS")],
            "cursor": "c1",
        }
        t1_done = {
            "conversation": {"status": "IN_PROGRESS"},
            "tool_calls": [_tool_row("t1", "SUCCESS")],
            "cursor": "c2",
        }
        t2_running = {
            "conversation": {"status": "IN_PROGRESS"},
            "tool_calls": [_tool_row("t2", "IN_PROGRESS")],
            "cursor": "c3",
        }
        t2_done = {"conversation": {"status": "COMPLETED"}, "tool_calls": [_tool_row("t2", "SUCCESS")], "cursor": "c4"}
        # t2_done twice: once for the main loop, once for the finally block's
        # own re-fetch once it decides the terminal status closes the panel.
        self._run(common, cache, deltas=[t1_running, t1_done, t2_running, t2_done, t2_done])
        appended = common.slack_app.client.append_stream.call_args_list
        starting = slack_progress._INITIAL_TASK_ID

        def placeholder_chunk(call):
            return next(c for c in call.kwargs["chunks"] if c["id"] == starting)

        # t1 starts: synthetic task settles under its original title.
        assert placeholder_chunk(appended[0]) == {
            "type": "task_update",
            "id": starting,
            "title": slack_progress._INITIAL_TASK_TITLE,
            "status": "complete",
        }
        # t1 finishes, nothing else active: reopens, relabeled.
        assert placeholder_chunk(appended[1]) == {
            "type": "task_update",
            "id": starting,
            "title": slack_progress._CONTINUING_TASK_TITLE,
            "status": "in_progress",
        }
        # t2 starts: settles again, keeping the relabeled title (no flapping
        # back to "Understanding your query").
        assert placeholder_chunk(appended[2]) == {
            "type": "task_update",
            "id": starting,
            "title": slack_progress._CONTINUING_TASK_TITLE,
            "status": "complete",
        }
        # t2 finishes and the turn is over, but this poll iteration still
        # sees nothing active — reopens once more before the loop exits.
        assert placeholder_chunk(appended[3]) == {
            "type": "task_update",
            "id": starting,
            "title": slack_progress._CONTINUING_TASK_TITLE,
            "status": "in_progress",
        }
        # The finally block's flush is what actually closes the panel out.
        assert placeholder_chunk(appended[4]) == {
            "type": "task_update",
            "id": starting,
            "title": slack_progress._INITIAL_TASK_TITLE,
            "status": "complete",
        }
        common.slack_app.client.stop_stream.assert_called_once()

    def test_waiting_for_client_tool_does_not_stop_the_panel_mid_run(self):
        """A delegate/sub-agent step can leave the conversation reading WAITING
        or WAITING_FOR_CLIENT_TOOL without a real end-user follow-up pending —
        neither is terminal in llm-server's own model, so the poller must keep
        going past it instead of closing the panel mid-investigation."""
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}] + [{"stream_ts": "3000.3"}] * 4
        cache.update_event_entry.return_value = True
        deltas = [
            {
                "conversation": {"status": "WAITING_FOR_CLIENT_TOOL"},
                "tool_calls": [_tool_row("t1", "IN_PROGRESS")],
                "cursor": "c1",
            },
            {"conversation": {"status": "WAITING"}, "tool_calls": [_tool_row("t1", "SUCCESS")], "cursor": "c2"},
            {"conversation": {"status": "COMPLETED"}, "tool_calls": [_tool_row("t2", "SUCCESS")], "cursor": "c3"},
        ]
        self._run(common, cache, deltas=deltas)
        appended = common.slack_app.client.append_stream.call_args_list
        # All three deltas were processed — the two non-terminal-for-llm-server
        # statuses didn't cut the loop short — and the panel only closes once,
        # on the truly terminal COMPLETED delta. The 4th call is the finally
        # block's guaranteed placeholder finalize.
        assert len(appended) == 4
        common.slack_app.client.stop_stream.assert_called_once()

    def test_stale_completed_status_before_turn_activity_is_ignored(self):
        """On a reused Slack thread, the very first poll can still read
        COMPLETED left over from the previous turn (llm-server's async
        endpoint returns 202 before a worker dequeues the request and flips
        the row's status). That stale read must not close the panel — only a
        COMPLETED seen after real activity (a message or tool call) for this
        turn should."""
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}] + [{"stream_ts": "3000.3"}] * 4
        cache.update_event_entry.return_value = True
        deltas = [
            {"conversation": {"status": "COMPLETED"}, "tool_calls": [], "cursor": "c1"},
            {"conversation": {"status": "IN_PROGRESS"}, "tool_calls": [_tool_row("t1", "IN_PROGRESS")], "cursor": "c2"},
            {"conversation": {"status": "COMPLETED"}, "tool_calls": [_tool_row("t1", "SUCCESS")], "cursor": "c3"},
        ]
        self._run(common, cache, deltas=deltas)
        appended = common.slack_app.client.append_stream.call_args_list
        # All three deltas were processed — the stale COMPLETED on the first
        # poll didn't cut the loop short — and the panel only closes once,
        # on the COMPLETED that arrives after real activity was observed. The
        # first (empty) delta has nothing to show, so it appends nothing —
        # the synthetic task stays in_progress rather than completing early.
        # The 3rd call is the finally block's guaranteed placeholder finalize.
        assert len(appended) == 3
        common.slack_app.client.stop_stream.assert_called_once()

    def test_ack_only_message_does_not_count_as_turn_activity(self):
        """Reproduces a live incident (2026-08-19): on a reused Slack thread,
        llm-server inserts the new turn's message row with an empty response
        and attaches its ack_message via a separate update that never touches
        response/status - well before a worker actually starts real work and
        the stale COMPLETED status (left over from the previous turn) gets
        overwritten. A bare message row with no response must not satisfy the
        turn-activity guard, or the still-stale COMPLETED gets trusted and
        the panel closes before the investigation (here: 19 real tool calls)
        ever starts."""
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}] + [{"stream_ts": "3000.3"}] * 4
        cache.update_event_entry.return_value = True
        deltas = [
            # The ack-only write: a new message row, still no response, and
            # the conversation status hasn't flipped off COMPLETED yet.
            {
                "conversation": {"status": "COMPLETED"},
                "messages": [{"id": "m1", "response": ""}],
                "tool_calls": [],
                "cursor": "c1",
            },
            # Real work starts.
            {"conversation": {"status": "IN_PROGRESS"}, "tool_calls": [_tool_row("t1", "IN_PROGRESS")], "cursor": "c2"},
            {"conversation": {"status": "COMPLETED"}, "tool_calls": [_tool_row("t1", "SUCCESS")], "cursor": "c3"},
        ]
        self._run(common, cache, deltas=deltas)
        appended = common.slack_app.client.append_stream.call_args_list
        # All three deltas were processed - the ack-only first delta didn't
        # cut the loop short - and the panel only closes once, on the
        # COMPLETED that arrives after the real tool call was observed. The
        # 3rd call is the finally block's guaranteed placeholder finalize.
        assert len(appended) == 3
        common.slack_app.client.stop_stream.assert_called_once()

    def test_poller_self_close_still_flushes_a_tool_call_that_missed_the_last_poll(self):
        """A tool call can finish in the same window the poller decides the
        turn is done (a live incident: the conversation read COMPLETED before
        the turn's own final write), so when the poller closes the panel
        itself — not via the external settle handler — it must still take one
        more look before actually stopping, exactly like stop_progress_stream
        already does."""
        common = self._common()
        cache = MagicMock()
        cache.get_event_entry.side_effect = [{}, {"stream_ts": "pending-fixed"}] + [{"stream_ts": "3000.3"}] * 3
        cache.update_event_entry.return_value = True
        deltas = [
            {"conversation": {"status": "IN_PROGRESS"}, "tool_calls": [_tool_row("t1", "IN_PROGRESS")], "cursor": "c1"},
            {"conversation": {"status": "COMPLETED"}, "tool_calls": [], "cursor": "c2"},
            # Only reached by the finally-block flush, after the main loop
            # already decided to close on the (trusted, but incomplete) delta
            # above.
            {"tool_calls": [_tool_row("t2", "SUCCESS")]},
        ]
        self._run(common, cache, deltas=deltas)
        appended = common.slack_app.client.append_stream.call_args_list
        assert len(appended) == 2
        assert appended[-1].kwargs["chunks"][0] == {
            "type": "task_update",
            "id": "t2",
            "title": "Checking logs",
            "status": "complete",
        }
        common.slack_app.client.stop_stream.assert_called_once()
