"""Archive steps must target only the status the scanner owns.

The scanner owns Open and Archive. Every other status — Dismissed/snoozed,
InProgress, Closed, Assigned — is set by a person and has to survive the next
scan. Both archive sweeps here key off the scan's keep-set, but leaving the
keep-set does not prove a workload was deleted: a scan can cover one and emit no
row for it because nothing needs changing. Tombstoning a user-set status on that
pass meant the next pass that did emit a row saw 'Archive', treated it as
scanner-owned, and reopened a finding the user had already triaged.

The sweeps therefore name 'Open' rather than excluding the statuses they must not
touch. An exclusion list has to be extended every time a triage state is added
and silently stops covering the ones it misses — it already missed 'Assigned',
which read paths treat as active alongside Open.

The predicates live inside f-string SQL built at call time, so these assert over
the module source rather than executing the functions, which would need a live
engine. That is enough to catch the regression, which is always the archive
widening beyond Open.
"""

import os
import re
import unittest

_REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))

SOURCES = {
    "vertical_rightsizing": os.path.join(_REPO_ROOT, "server", "recommendation", "vertical_rightsizing", "__init__.py"),
    "volume_rightsizing": os.path.join(_REPO_ROOT, "server", "recommendation", "volume_rightsizing.py"),
}

# An archive sweep: UPDATE recommendation [alias] SET status = 'Archive'.
# Whitespace-tolerant so a reformatted statement is still examined rather than
# silently skipped, which would leave this test passing while checking nothing.
ARCHIVE_STATEMENT_RE = re.compile(r"(?i)UPDATE\s+recommendation(?:\s+\w+)?\s+SET\s+status\s*=\s*'Archive'")

# The only legal row selector for such a sweep.
OPEN_ONLY_RE = re.compile(r"(?i)\b(?:\w+\.)?status\s*=\s*'Open'")

# The pattern this replaced. Kept as an explicit negative so a future edit cannot
# quietly reintroduce a deny-list.
EXCLUSION_LIST_RE = re.compile(r"(?i)\b(?:\w+\.)?status\s+NOT\s+IN\s*\(")

# The upsert half of the same invariant.
UPSERT_GUARD_RE = re.compile(
    r"(?i)status\s*=\s*CASE\s+WHEN\s+recommendation\.status\s+NOT\s+IN\s*\(\s*'Open'\s*,\s*'Archive'\s*\)"
)
BARE_UPSERT_RE = re.compile(r"(?i)status\s*=\s*EXCLUDED\.status")

# Every archive sweep known to exist. A drop below this means the regex stopped
# matching and the test is no longer checking anything.
MIN_ARCHIVE_SWEEPS = 3


def _read(path):
    with open(path, encoding="utf-8") as handle:
        return handle.read()


def _statement_body(source, start):
    """The SQL statement beginning at start, bounded by its closing triple quote."""
    end = source.find('"""', start)
    return source[start:end] if end > start else source[start:]


class TestArchiveTargetsOpenOnly(unittest.TestCase):
    def test_every_archive_sweep_selects_open_only(self):
        found = 0
        for name, path in SOURCES.items():
            source = _read(path)
            for match in ARCHIVE_STATEMENT_RE.finditer(source):
                found += 1
                body = _statement_body(source, match.start())
                self.assertRegex(
                    body,
                    OPEN_ONLY_RE,
                    f"{name}: archive sweep does not select status = 'Open', so it can tombstone a "
                    f"row a person triaged and the next scan will reopen it. Statement was:\n{body}",
                )
                self.assertNotRegex(
                    body,
                    EXCLUSION_LIST_RE,
                    f"{name}: archive sweep selects rows by excluding statuses. That list has to be "
                    f"extended for every triage state added later and already missed 'Assigned'; "
                    f"name the scanner-owned status instead. Statement was:\n{body}",
                )
        self.assertGreaterEqual(
            found,
            MIN_ARCHIVE_SWEEPS,
            f"found only {found} archive sweeps; the pattern has probably drifted "
            "and this test is no longer checking anything",
        )


class TestUpsertsPreserveUserOwnedStates(unittest.TestCase):
    def test_no_bare_status_assignment(self):
        for name, path in SOURCES.items():
            source = _read(path)
            for match in BARE_UPSERT_RE.finditer(source):
                # The legal form is the CASE guard, whose ELSE branch also ends in
                # EXCLUDED.status; accept only that.
                window_start = max(0, match.start() - 160)
                self.assertRegex(
                    source[window_start : match.end()],
                    UPSERT_GUARD_RE,
                    f"{name}: an upsert assigns status straight from EXCLUDED, which reopens "
                    f"findings the archive just tombstoned",
                )

    def test_guards_are_present(self):
        for name, path in SOURCES.items():
            self.assertRegex(
                _read(path),
                UPSERT_GUARD_RE,
                f"{name}: expected a guarded recommendation upsert and found none",
            )


if __name__ == "__main__":
    unittest.main()
