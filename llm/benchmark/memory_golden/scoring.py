"""Scoring for the memory_write golden set: one result per case, a summary per provider."""

import re
from dataclasses import dataclass, field

RECALL_FLOOR = 0.7
MAX_FALSE_SAVES = 0
_SEPARATORS = re.compile(r"[-_\s]+")


def fold(text: str) -> str:
    """Lowercase and treat '-', '_' and spaces as one space, as memory_read does."""
    return _SEPARATORS.sub(" ", text.lower()).strip()


@dataclass
class Write:
    args: dict
    accepted: bool


@dataclass
class CaseResult:
    case_id: str
    expect: str
    # correct | type_mixup | missed | wrong_type | wrong_content | refused
    # | false_save | refused_save | quiet | error
    outcome: str
    latency_s: float = 0.0
    detail: str = ""


def _haystack(args: dict) -> str:
    parts = []
    for name, value in args.items():
        if name in ("evidence", "type") or value is None:
            continue
        if isinstance(value, list):
            parts.extend(str(v) for v in value if v is not None)
        else:
            parts.append(str(value))
    return fold(" ".join(parts))


def content_matches(case: dict, args: dict) -> bool:
    hay = _haystack(args)
    required = [fold(t) for t in case.get("must_contain", [])]
    any_of = [fold(t) for t in case.get("must_contain_any", [])]
    if any(t not in hay for t in required):
        return False
    return not any_of or any(t in hay for t in any_of)


def name_matches(case: dict, name: str) -> bool:
    """Whole-word version of content_matches for a row's key or subject."""
    words = f" {fold(name)} "
    required = [f" {fold(t)} " for t in case.get("must_contain", [])]
    any_of = [f" {fold(t)} " for t in case.get("must_contain_any", [])]
    if any(t not in words for t in required):
        return False
    return not any_of or any(t in words for t in any_of)


def score_case(case: dict, writes: list[Write]) -> str:
    """Outcome for one case given the memory_write calls the agent made."""
    accepted = [w for w in writes if w.accepted]
    if case["expect"] == "none":
        if accepted:
            return "false_save"
        return "refused_save" if writes else "quiet"
    if not writes:
        return "missed"
    if not accepted:
        return "refused"
    typed = [w for w in accepted if w.args.get("type") == case["expect"]]
    if not typed:
        # The right content saved under another type still reaches memory_read;
        # it counts toward recall and is reported on its own.
        if any(content_matches(case, w.args) for w in accepted):
            return "type_mixup"
        return "wrong_type"
    if not any(content_matches(case, w.args) for w in typed):
        return "wrong_content"
    return "correct"


@dataclass
class ProviderSummary:
    provider: str
    source: str = ""
    run: int = 1
    results: list[CaseResult] = field(default_factory=list)

    def _count(self, *outcomes: str, expect_none: bool | None = None) -> int:
        return sum(
            1
            for r in self.results
            if r.outcome in outcomes
            and (expect_none is None or (r.expect == "none") == expect_none)
        )

    @property
    def errors(self) -> int:
        return self._count("error")

    @property
    def expected_saves(self) -> int:
        return sum(
            1 for r in self.results if r.expect != "none" and r.outcome != "error"
        )

    @property
    def correct(self) -> int:
        return self._count("correct", "type_mixup")

    @property
    def negatives(self) -> int:
        return sum(
            1 for r in self.results if r.expect == "none" and r.outcome != "error"
        )

    @property
    def false_saves(self) -> int:
        return self._count("false_save")

    @property
    def recall(self) -> float:
        return self.correct / self.expected_saves if self.expected_saves else 0.0

    @property
    def false_save_rate(self) -> float:
        return self.false_saves / self.negatives if self.negatives else 0.0

    @property
    def avg_latency_s(self) -> float:
        done = [r.latency_s for r in self.results if r.outcome != "error"]
        return sum(done) / len(done) if done else 0.0

    @property
    def verdict(self) -> str:
        if self.errors:
            return "INCOMPLETE"
        if self.recall < RECALL_FLOOR or self.false_saves > MAX_FALSE_SAVES:
            return "FAIL"
        return "PASS"

    def as_dict(self) -> dict:
        return {
            "provider": self.provider,
            "llm_config_source": self.source,
            "run": self.run,
            "verdict": self.verdict,
            "recall": round(self.recall, 3),
            "correct": self.correct,
            "expected_saves": self.expected_saves,
            "missed": self._count("missed"),
            "refused": self._count("refused"),
            "type_mixups": self._count("type_mixup"),
            "wrong_type": self._count("wrong_type"),
            "wrong_content": self._count("wrong_content"),
            "false_saves": self.false_saves,
            "negatives": self.negatives,
            "false_save_rate": round(self.false_save_rate, 3),
            "refused_saves": self._count("refused_save"),
            "errors": self.errors,
            "avg_latency_s": round(self.avg_latency_s, 1),
            "cases": [r.__dict__ for r in self.results],
        }


def render_table(summaries: list[ProviderSummary]) -> str:
    header = (
        "| Provider | Result | Recall | Type mix-ups | Missed | Refused | Wrong type | Wrong content "
        "| False saves | Refused saves | Errors | Avg latency |"
    )
    rows = [header, "|" + "---|" * 12]
    for s in summaries:
        name = s.provider if s.run == 1 else f"{s.provider} (run {s.run})"
        rows.append(
            f"| {name} | {s.verdict} | {s.recall:.0%} ({s.correct}/{s.expected_saves}) "
            f"| {s._count('type_mixup')} | {s._count('missed')} | {s._count('refused')} | {s._count('wrong_type')} "
            f"| {s._count('wrong_content')} | {s.false_saves}/{s.negatives} ({s.false_save_rate:.0%}) "
            f"| {s._count('refused_save')} | {s.errors} | {s.avg_latency_s:.1f}s |"
        )
    return "\n".join(rows)


def render_repeats(summaries: list[ProviderSummary]) -> str:
    """Worst and mean per provider across repeated runs."""
    by_provider: dict[str, list[ProviderSummary]] = {}
    for s in summaries:
        by_provider.setdefault(s.provider, []).append(s)
    rows = [
        "| Provider | Runs | Verdicts | Worst recall | Mean recall | Most false saves |",
        "|" + "---|" * 6,
    ]
    for name, runs in by_provider.items():
        recalls = [r.recall for r in runs]
        rows.append(
            f"| {name} | {len(runs)} | {' '.join(r.verdict for r in runs)} "
            f"| {min(recalls):.0%} | {sum(recalls) / len(recalls):.0%} "
            f"| {max(r.false_saves for r in runs)} |"
        )
    return "\n".join(rows)
