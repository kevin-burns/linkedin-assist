"""Stdlib-only tests for li_report. No network, no LinkedIn session, and no
touching the real ~/.config/li-assist/ -- every test points li_report's
CACHE_DEFAULT (a module global, patched directly -- see li_report.main()'s
comment on why it must be a global rather than a bound default) and passes
an explicit --config at a tempfile fixture. The two subprocess-based tests
(TestMainAsScript, TestBrokenPipe) genuinely execve a fresh interpreter --
HOME is injected via `env` for those instead, since CACHE_DEFAULT resolves
from Path.home() at import time in that fresh process.
"""

import html
import io
import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from datetime import date, datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import li_digest  # noqa: E402
import li_report  # noqa: E402


GOOD_CONFIG = {
    "defaults": {"location": "Germany", "limit": 50},
    "archetypes": [
        {"name": "platform", "label": "Platform",
         "query": '"platform engineer"', "match": "platform|terraform"},
        {"name": "em", "label": "EM",
         "query": '"engineering manager"', "match": "engineering manager|tech lead"},
    ],
}


# Relative, NOT a literal date. This default was "2026-08-04T00:00:00Z", and
# li-report windows against the real clock (select_rows' `today` seam is only
# reachable from unit tests, not from the CLI ones). On 2026-08-19 the fixture
# turned 15 days old, fell out of the default 14-day window, and four CLI tests
# started failing on a calendar boundary with no code change on any branch --
# reading, wrongly, as though the PR under review had broken them.
#
# Two days keeps it comfortably inside every window the suite exercises while
# still being genuinely "recent". Tests that care about a particular date --
# the old-bucket and sort-order cases -- pass `posted=` explicitly and are
# unaffected.
DEFAULT_POSTED = (datetime.now(timezone.utc) - timedelta(days=2)).strftime(
    "%Y-%m-%dT%H:%M:%SZ"
)


def cache_job(urn, title, company="Acme", posted=DEFAULT_POSTED,
              location="Germany (Remote)", description=None):
    row = {
        "urn": f"urn:li:fsd_jobPosting:{urn}",
        "title": title,
        "location": location,
        "company": {"urn": "", "name": company},
        "posted_at": posted,
    }
    if description is not None:
        row["description"] = description
    return row


def load_cfg(data):
    """load_config needs a real file; this hides the tempdir plumbing for
    tests that only need a Config object, not a config PATH."""
    with tempfile.TemporaryDirectory() as d:
        path = Path(d) / "archetypes.json"
        path.write_text(json.dumps(data), encoding="utf-8")
        return li_digest.load_config(path)


class ReportTempDir(unittest.TestCase):
    """Isolated config dir + isolated cache file per test."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)
        self.addCleanup(self._tmp.cleanup)
        self.cache_path = self.tmp / "jobs.jsonl"
        patcher = mock.patch.object(li_report, "CACHE_DEFAULT", self.cache_path)
        patcher.start()
        self.addCleanup(patcher.stop)

    def write_config(self, data, name="archetypes.json"):
        path = self.tmp / name
        path.write_text(json.dumps(data), encoding="utf-8")
        return path

    def write_cache(self, rows):
        text = "\n".join(json.dumps(r) for r in rows)
        if rows:
            text += "\n"
        self.cache_path.write_text(text, encoding="utf-8")


class TestLoadCache(ReportTempDir):

    def test_missing_file_is_a_config_error(self):
        with self.assertRaises(li_digest.ConfigError):
            li_report.load_cache(self.tmp / "nope.jsonl")

    def test_blank_lines_are_skipped(self):
        self.cache_path.write_text(
            '\n{"urn": "urn:li:fsd_jobPosting:1", "title": "X"}\n\n', encoding="utf-8"
        )
        self.assertEqual(len(li_report.load_cache(self.cache_path)), 1)

    def test_empty_file_is_an_empty_list_not_an_error(self):
        self.cache_path.write_text("", encoding="utf-8")
        self.assertEqual(li_report.load_cache(self.cache_path), [])

    def test_malformed_json_line_raises_config_error(self):
        self.cache_path.write_text("not json\n", encoding="utf-8")
        with self.assertRaises(li_digest.ConfigError):
            li_report.load_cache(self.cache_path)

    def test_non_object_line_raises_config_error(self):
        self.cache_path.write_text("[1, 2, 3]\n", encoding="utf-8")
        with self.assertRaises(li_digest.ConfigError):
            li_report.load_cache(self.cache_path)

    def test_invalid_utf8_raises_config_error_not_unicode_decode_error(self):
        """UnicodeDecodeError is not an OSError -- catching only
        (FileNotFoundError, IsADirectoryError, PermissionError), or even
        the wider OSError alone, misses it entirely and lets it escape past
        `except ConfigError` in main() to an uncaught traceback."""
        self.cache_path.write_bytes(b"\xff\xfe not valid utf-8\n")
        with self.assertRaises(li_digest.ConfigError):
            li_report.load_cache(self.cache_path)

    def test_parent_path_is_a_regular_file_raises_config_error(self):
        """NotADirectoryError -- a path whose parent component is itself a
        regular file, not a directory. It IS an OSError (unlike
        UnicodeDecodeError above), but the original narrower tuple named
        only FileNotFoundError/IsADirectoryError/PermissionError and missed
        this sibling."""
        blocker = self.tmp / "blocker"
        blocker.write_text("not a directory", encoding="utf-8")
        with self.assertRaises(li_digest.ConfigError):
            li_report.load_cache(blocker / "jobs.jsonl")


class TestSelectRows(ReportTempDir):

    def setUp(self):
        super().setUp()
        self.cfg = li_digest.load_config(self.write_config(GOOD_CONFIG))
        self.today = date(2026, 8, 5)

    def test_old_bucket_is_excluded(self):
        rows = [cache_job("1", "Platform Engineer", posted="2020-01-01T00:00:00Z")]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(kept, [])

    def test_in_window_row_is_kept(self):
        rows = [cache_job("1", "Platform Engineer", posted="2026-08-01T00:00:00Z")]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(len(kept), 1)
        self.assertEqual(kept[0]["bucket"], "in")

    def test_undated_row_is_kept_and_not_bucketed_as_in_or_old(self):
        rows = [cache_job("1", "Platform Engineer", posted=li_digest.ZERO_DATE)]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(len(kept), 1)
        self.assertEqual(kept[0]["bucket"], "undated")

    def test_excluded_company_is_dropped_case_insensitively(self):
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["exclude_company"] = ["acme"]
        cfg = li_digest.load_config(self.write_config(data, "ex.json"))
        rows = [
            cache_job("1", "Platform Engineer", company="ACME Corp"),
            cache_job("2", "Platform Engineer", company="Globex"),
        ]
        kept = li_report.select_rows(rows, cfg, 14, today=self.today)
        self.assertEqual([r["urn"] for r in kept], ["urn:li:fsd_jobPosting:2"])

    def test_excluded_title_is_dropped_case_insensitively(self):
        """The sibling of the company test above, and it was missing.

        li-digest filters titles at FETCH time by passing --exclude-title to
        `li-assist jobs sweep`, which does nothing for rows already in the
        cache. Measured 2026-08-28: rows matching `junior` and `werkstudent`
        -- terms that had been in exclude_title for weeks -- were still
        rendering, because only exclude_company was applied here.
        """
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["exclude_title"] = ["ai trainer", "JUNIOR"]
        cfg = li_digest.load_config(self.write_config(data, "extitle.json"))
        rows = [
            cache_job("1", "Platform Engineer - AI Trainer (Freelance)"),
            cache_job("2", "Junior Platform Engineer"),
            cache_job("3", "Senior Platform Engineer"),
        ]
        kept = li_report.select_rows(rows, cfg, 14, today=self.today)
        self.assertEqual([r["urn"] for r in kept], ["urn:li:fsd_jobPosting:3"])

    def test_title_and_company_exclusions_apply_together(self):
        """Neither list may shadow the other."""
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["exclude_title"] = ["ai trainer"]
        data["defaults"]["exclude_company"] = ["acme"]
        cfg = li_digest.load_config(self.write_config(data, "exboth.json"))
        rows = [
            cache_job("1", "AI Trainer", company="Globex"),
            cache_job("2", "Platform Engineer", company="ACME Corp"),
            cache_job("3", "Platform Engineer", company="Globex"),
        ]
        kept = li_report.select_rows(rows, cfg, 14, today=self.today)
        self.assertEqual([r["urn"] for r in kept], ["urn:li:fsd_jobPosting:3"])

    def test_empty_exclude_title_keeps_everything(self):
        """A guard that blocks when unconfigured is worse than no guard."""
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["exclude_title"] = []
        cfg = li_digest.load_config(self.write_config(data, "exnone.json"))
        rows = [cache_job("1", "AI Trainer"), cache_job("2", "Platform Engineer")]
        kept = li_report.select_rows(rows, cfg, 14, today=self.today)
        self.assertEqual(len(kept), 2)

    def test_row_with_no_title_does_not_raise(self):
        """Cache rows have carried non-string fields before -- see the
        posted_at test below. A missing title must not crash the render."""
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["exclude_title"] = ["ai trainer"]
        cfg = li_digest.load_config(self.write_config(data, "exnotitle.json"))
        rows = [cache_job("1", "Platform Engineer")]
        rows[0]["title"] = None
        li_report.select_rows(rows, cfg, 14, today=self.today)

    def test_mixed_string_and_non_string_posted_at_does_not_raise(self):
        """A cache with a non-string posted_at (e.g. an int, which real
        cache data has produced before -- see li_digest's own comment on
        this exact defect class in _cells) alongside any string-dated row
        used to raise `TypeError: '<' not supported between instances of
        'int' and 'str'` mid-sort, escaping the `except ConfigError` catch
        in main() to an uncaught traceback and exit 1 -- outside the
        documented 0/2 contract."""
        rows = [
            cache_job("1", "Platform Engineer", posted="2026-08-01T00:00:00Z"),
        ]
        rows.append(dict(cache_job("2", "Platform Engineer"), posted_at=20260804))
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(len(kept), 2)

    def test_newest_posting_sorts_first_even_when_it_matches_fewer_lanes(self):
        """Posted date is the PRIMARY sort key; lane count only breaks ties.

        This was the other way round until 2026-08-10: lane count led, so
        every multi-lane row outranked every single-lane one no matter how
        old it was. At 359 rows that read as a shortlist; once the corpus
        reached ~580 the top of the report was stale three-lane matches
        while the newest postings sat pages down -- under a column headed
        `Posted`. The multi-lane view does not need to own the sort order:
        the report already ships a `Multi-lane` checkbox that filters to
        exactly that set.
        """
        rows = [
            # two lanes (platform + em), older
            cache_job("old", "Engineering Manager, Platform",
                      posted="2026-07-29T00:00:00Z"),
            # one lane (platform), newer
            cache_job("new", "Platform Engineer",
                      posted="2026-08-04T00:00:00Z"),
        ]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(
            [r["urn"] for r in kept],
            ["urn:li:fsd_jobPosting:new", "urn:li:fsd_jobPosting:old"],
            "newest posting must sort first even though it matches fewer lanes",
        )

    def test_lane_count_still_breaks_ties_on_equal_dates(self):
        """Lane count survives as the tiebreaker, so same-day rows still
        surface the multi-lane match first. Without this the sort would be
        date-only and the multi-lane signal would be lost outright."""
        rows = [
            cache_job("one", "Platform Engineer", posted="2026-08-04T00:00:00Z"),
            cache_job("two", "Engineering Manager, Platform",
                      posted="2026-08-04T00:00:00Z"),
        ]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(
            [r["urn"] for r in kept],
            ["urn:li:fsd_jobPosting:two", "urn:li:fsd_jobPosting:one"],
            "on equal dates the row matching more lanes must come first",
        )

    def test_lane_count_breaks_ties_within_the_same_DISPLAYED_date(self):
        """The tiebreaker must fire on the date the reader can SEE.

        `posted_at` carries a full timestamp but `_posted_cell` renders only
        `posted_at[:10]`. Sorting on the whole string made time-of-day the
        real tiebreaker, so lane count effectively never fired: a live cache
        had 17 distinct timestamps across the 18 rows sharing one date. The
        page showed a column of identical dates ordered by an invisible
        field, and the README claim that a same-day multi-lane row outranks
        a single-lane one was simply untrue.

        Here the ONE-lane row carries the later timestamp, so it wins on
        the full string and loses on the date. Sorting on the full string
        puts `late` first and fails this test.
        """
        rows = [
            cache_job("late", "Platform Engineer",
                      posted="2026-08-04T23:59:00Z"),          # 1 lane, later
            cache_job("multi", "Engineering Manager, Platform",
                      posted="2026-08-04T00:01:00Z"),          # 2 lanes, earlier
        ]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(
            [r["urn"] for r in kept],
            ["urn:li:fsd_jobPosting:multi", "urn:li:fsd_jobPosting:late"],
            "within one displayed date, lane count must decide -- not time of day",
        )

    def test_undated_rows_sort_after_every_dated_row(self):
        """Undated rows are KEPT (a missing date is not evidence a job is
        stale) but they cannot be ranked against dates they do not have, so
        they go last rather than being guessed into the recent end. This is
        deliberate and asserted, because it is otherwise a silent product
        change: before date led the sort, a multi-lane undated row could
        rank near the top."""
        rows = [
            cache_job("undated", "Engineering Manager, Platform",
                      posted=li_digest.ZERO_DATE),             # 2 lanes, undated
            cache_job("dated", "Platform Engineer",
                      posted="2026-07-29T00:00:00Z"),          # 1 lane, oldest dated
        ]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(
            [r["urn"] for r in kept],
            ["urn:li:fsd_jobPosting:dated", "urn:li:fsd_jobPosting:undated"],
            "an undated row must not outrank a dated one, whatever it matches",
        )

    def test_undated_rows_are_ordered_among_themselves_by_lane_count(self):
        """Both undated shapes must sort identically.

        `bucket_of` treats a missing `posted_at` and the ZERO_DATE sentinel
        as the same thing, but they used to produce different sort keys
        ("" vs "0001-01-01T00:00:00Z"), so the sentinel outranked the
        missing value for no reason a reader could see. Normalising them
        lets lane count order the undated group instead of the accident of
        which shape the cache happened to store.

        The sentinel row deliberately carries FEWER lanes here. If the raw
        strings still decide, "0001-01-01T00:00:00Z" beats "" and the
        one-lane sentinel leads; only normalising both to the same key lets
        lane count put the two-lane row first. An earlier version of this
        test had it the other way round and passed on the sentinel winning,
        proving nothing.
        """
        rows = [
            cache_job("sentinel1", "Platform Engineer",
                      posted=li_digest.ZERO_DATE),               # 1 lane
            dict(cache_job("none2", "Engineering Manager, Platform"),
                 posted_at=None),                                # 2 lanes
        ]
        kept = li_report.select_rows(rows, self.cfg, 14, today=self.today)
        self.assertEqual(
            [r["urn"] for r in kept],
            ["urn:li:fsd_jobPosting:none2", "urn:li:fsd_jobPosting:sentinel1"],
            "the two-lane undated row must lead, whichever undated shape it uses",
        )


class TestWorkplace(unittest.TestCase):
    """li_digest._is_remote's `(Remote)` marker check is case-insensitive;
    an earlier version of _workplace's regex was not, silently diverging
    from that convention and undercounting rows like "Berlin (remote)"."""

    def test_matches_case_insensitively(self):
        self.assertEqual(li_report._workplace("Berlin (remote)"), ("Remote", "Berlin"))
        self.assertEqual(li_report._workplace("Berlin (REMOTE)"), ("Remote", "Berlin"))

    def test_canonical_casing_regardless_of_source_casing(self):
        self.assertEqual(li_report._workplace("X (HYBRID)"), ("Hybrid", "X"))
        self.assertEqual(li_report._workplace("X (on-SITE)"), ("On-site", "X"))

    def test_no_marker_is_unknown(self):
        self.assertEqual(li_report._workplace("Berlin"), ("unknown", "Berlin"))

    def test_non_string_location_is_unknown_not_a_crash(self):
        self.assertEqual(li_report._workplace(None), ("unknown", ""))


class TestUrnTail(unittest.TestCase):
    """_urn_tail feeds two unquoted-then-fixed attribute sites (the title
    link's #fragment and the details id). Quoting the attribute at the call
    site is only half the fix -- see li_report.py's docstring on this
    function -- the other half is constraining the character set here, at
    the source."""

    def test_strips_everything_outside_the_safe_charset(self):
        row = {"urn": "urn:li:fsd_jobPosting:1 onmouseover=alert(1) x"}
        self.assertEqual(li_report._urn_tail(row), "1onmouseoveralert1x")

    def test_a_clean_numeric_tail_is_unchanged(self):
        row = {"urn": "urn:li:fsd_jobPosting:4431723620"}
        self.assertEqual(li_report._urn_tail(row), "4431723620")

    def test_missing_urn_is_empty_not_a_crash(self):
        self.assertEqual(li_report._urn_tail({}), "")

    def test_non_string_urn_is_empty_not_a_crash(self):
        self.assertEqual(li_report._urn_tail({"urn": 12345}), "")


class TestRenderHtmlBasics(unittest.TestCase):

    def _cfg(self, exclude_company=None):
        data = json.loads(json.dumps(GOOD_CONFIG))
        if exclude_company:
            data["defaults"]["exclude_company"] = exclude_company
        return load_cfg(data)

    def test_empty_result_set_produces_valid_html(self):
        cfg = self._cfg()
        doc = li_report.render_html([], cfg, 14, "smoke")
        self.assertTrue(doc.startswith("<!doctype html>"))
        self.assertTrue(doc.rstrip().endswith("</html>"))
        # Well-formed enough that the table/kpi scaffolding is present even
        # with nothing to show -- not a bare crash-avoidance stub.
        self.assertIn("<table id=t>", doc)
        self.assertIn("Prospects", doc)

    def test_no_external_cdn_or_stylesheet_references(self):
        """Self-contained means no network fetch at render time. Job links
        to linkedin.com are expected and fine (they are the point of the
        report) -- this checks specifically for an external stylesheet or
        script reference, not for the mere presence of 'http://'."""
        cfg = self._cfg()
        rows = li_digest.enrich_rows(
            [cache_job("1", "Platform Engineer")], cfg, date(2020, 1, 1)
        )
        doc = li_report.render_html(rows, cfg, 14, "smoke")
        self.assertNotIn("<link", doc)
        self.assertNotRegex(doc, r"<script[^>]+src=")
        self.assertNotIn("cdn.", doc)
        # The only <style>/<script> tags are the report's own inline ones.
        self.assertEqual(doc.count("<style>"), 1)
        self.assertEqual(doc.count("<script>"), 1)

    def test_undated_row_shows_no_fabricated_date(self):
        cfg = self._cfg()
        rows = li_digest.enrich_rows(
            [cache_job("1", "Platform Engineer", posted=li_digest.ZERO_DATE)],
            cfg, date(2020, 1, 1),
        )
        doc = li_report.render_html(rows, cfg, 14, "smoke")
        self.assertNotIn("0001-01-01", doc)
        self.assertNotIn("1900-01-01", doc)


class TestDeterminism(unittest.TestCase):
    """'No clock calls inside the builder' had no test: a render_html() that
    called datetime.now() internally instead of trusting the injected
    generated_at could still satisfy every other assertion in this file
    (test_generated_at_defaults_when_omitted only checks that the STRING
    "UTC" appears, which a clock call inside the renderer would also
    satisfy). Byte-identity across two independent calls with identical
    arguments is the actual claim being made."""

    def test_render_html_is_byte_identical_across_calls(self):
        cfg = load_cfg(GOOD_CONFIG)
        rows = li_digest.enrich_rows(
            [cache_job("1", "Platform Engineer")], cfg, date(2020, 1, 1)
        )
        doc1 = li_report.render_html(rows, cfg, 14, "smoke")
        doc2 = li_report.render_html(rows, cfg, 14, "smoke")
        self.assertEqual(doc1, doc2)
        self.assertEqual(doc1.encode("utf-8"), doc2.encode("utf-8"))

    def test_render_html_never_reads_the_clock(self):
        """Byte-identity alone is too weak to catch the realistic regression.

        Two back-to-back calls land in the same MINUTE, so injecting the
        production format -- datetime.now(timezone.utc).strftime(
        "%Y-%m-%d %H:%M UTC") -- into render_html leaves the byte-identity
        assertion above green. Patch the module's datetime and assert the
        builder never touches it at all.
        """
        cfg = load_cfg(GOOD_CONFIG)
        rows = li_digest.enrich_rows(
            [cache_job("1", "Platform Engineer")], cfg, date(2020, 1, 1)
        )
        real = li_report.datetime

        class _Tripwire:
            def __getattr__(self, name):
                raise AssertionError(
                    f"render_html read the clock via datetime.{name}; "
                    "generated_at is injected precisely so it cannot"
                )

        li_report.datetime = _Tripwire()
        try:
            li_report.render_html(rows, cfg, 14, "smoke")
        finally:
            li_report.datetime = real


# ---------------------------------------------------------------------------
# Hostile-input escaping: every _esc(...) call site in render_html, each
# with its own hostile value and its own targeted assertion. See the
# per-site falsification matrix in the round-1/round-2 fix reports for the
# "removed this one _esc() call -> this specific assertion failed, and only
# this one" evidence each of these is meant to reproduce.
# ---------------------------------------------------------------------------

# The archetype `name` and `exclude_company` are hostile too, because the
# footer interpolates both and they are the only two escape sites a safe
# config leaves unexercised. They are operator-controlled -- archetypes.json
# is your own file, not attacker input -- but li_digest.load_config
# constrains `name` only to a non-empty string and does not validate
# `exclude_company` at all, and the escaping claim is defence-in-depth, so
# it gets a test rather than an assertion in a docstring.
HOSTILE_CONFIG = {
    "defaults": {"exclude_company": ["<b>XE9</b>"]},
    "archetypes": [
        {"name": "<script>XN8</script>", "label": "<b>XA7</b>",
         "query": "hostile", "match": "XT1"},
    ],
}

HOSTILE_TITLE = "<script>XT1</script>"
HOSTILE_COMPANY = '"XC2 onmouseover="alert(1)'
HOSTILE_LOCATION = "<b>XL3</b> (Remote)"
HOSTILE_DESCRIPTION = '</div><img src=x onerror=alert(1)>XD4<b>Y</b> and a literal </script> too'
# Combines an unquoted-attribute breakout (space/=) with a quoted-attribute
# breakout (an embedded ' ) and a tag breakout (<b>...</b>) in one value,
# because both the local #fragment id (via the sanitising _urn_tail) and
# the external link href (via li_digest.job_link, UNSANITISED -- it slices
# the same tail from the raw urn) are derived from this exact string, and
# both need to be proven safe from it.
HOSTILE_URN_TAIL = "1 onmouseover=alert(1) XU5' onmouseover='alert(2) <b>XU5B</b>"
HOSTILE_GENERATED_AT = "<script>XG6</script>"


class TestHostileInputIsEscaped(unittest.TestCase):
    """Job titles, company names, locations, descriptions and urns are
    untrusted input straight from LinkedIn; archetype labels and
    --generated-at are operator-controlled but still routed through _esc
    defensively. Escaping is the single highest-risk part of this file.

    Falsification note: an earlier probe asserted on the bare substring
    'onerror=alert', which survives escaping as INERT TEXT when it lands in
    a TEXT NODE -- html.escape does not touch it there (no HTML
    metacharacters), so asserting on it in that context produces a false
    failure. That reasoning does NOT generalise to every context, though:
    the same-looking payload becomes LIVE when it lands in an UNQUOTED
    ATTRIBUTE instead -- html.escape(quote=True) escapes `& < > " '` but
    not space, `=`, `/` or a backtick, and an unquoted attribute value ends
    at the first space. The load-bearing assertion in a text-node context
    is on the tag itself ('<img' vs '&lt;img'); the load-bearing assertion
    at an attribute site is that the value round-trips through quoting
    intact (nothing broke the attribute boundary) AND, for the two
    #fragment sites specifically, that the character set was constrained
    at the source (see TestUrnTail and _urn_tail's docstring) rather than
    relying on quoting alone.
    """

    @classmethod
    def setUpClass(cls):
        cls.cfg = load_cfg(HOSTILE_CONFIG)
        row = cache_job(
            HOSTILE_URN_TAIL, HOSTILE_TITLE, company=HOSTILE_COMPANY,
            location=HOSTILE_LOCATION, description=HOSTILE_DESCRIPTION,
        )
        # A SECOND hostile row with NO description. The title cell forks on
        # `has_jd and jid`; a row carrying a description takes the anchor
        # branch, so a fixture where every row has one never exercises the
        # else-branch escape at all. That branch is the COMMON shape in
        # practice -- `jobs sweep` rows have no description until `jobs get`
        # is run for them, which was 428 of 430 rows in the live smoke test.
        # Without this row, deleting the else-branch _esc leaves the whole
        # suite green while a hostile title renders a live second <script>.
        no_jd = cache_job(
            HOSTILE_URN_TAIL + "9", HOSTILE_TITLE, company=HOSTILE_COMPANY,
            location=HOSTILE_LOCATION,
        )
        no_jd.pop("description", None)
        enriched = li_digest.enrich_rows([row, no_jd], cls.cfg, date(2020, 1, 1))
        cls.doc = li_report.render_html(enriched, cls.cfg, 14, HOSTILE_GENERATED_AT)

    # -- title: table cell + drawer summary --------------------------------

    def test_title_is_escaped_on_a_row_with_no_description(self):
        """Guards the else-branch of the title fork specifically.

        `test_title_is_escaped` passes on the anchor branch alone, so it
        cannot detect the else-branch escape being removed.

        Scoped to <tbody> and asserted EXACTLY, both deliberately. Doc-wide
        the escaped title appears three times -- two table cells plus the
        row-1 drawer summary -- so a doc-wide `>= 2` still passes with the
        else-branch escape removed, which is the inert-guard failure this
        test exists to avoid being an instance of. Inside <tbody> it is one
        per row, so 2 holds only if BOTH fork branches escape.
        """
        escaped = html.escape(HOSTILE_TITLE, quote=True)
        tbody = self.doc.split("<tbody>", 1)[1].split("</tbody>", 1)[0]
        self.assertEqual(tbody.count(escaped), 2)
        self.assertNotIn("<script>XT1</script>", self.doc)

    def test_title_is_escaped(self):
        self.assertNotIn("<script>XT1</script>", self.doc)
        self.assertIn(html.escape(HOSTILE_TITLE, quote=True), self.doc)

    def test_exactly_one_real_script_tag_the_reports_own(self):
        self.assertEqual(self.doc.count("<script>"), 1)
        self.assertEqual(self.doc.count("<script "), 0)

    # -- footer: archetype names + excluded companies -----------------------

    def test_footer_archetype_names_and_exclusions_are_escaped(self):
        """The two operator-controlled sites, which a safe config never
        reaches. Both interpolate into the footer; dropping either _esc
        renders a live second <script> or a live <b>."""
        footer = self.doc.split("<footer>", 1)[1].split("</footer>", 1)[0]
        self.assertIn(html.escape("<script>XN8</script>", quote=True), footer)
        self.assertIn(html.escape("<b>XE9</b>", quote=True), footer)
        self.assertNotIn("<script>XN8</script>", footer)
        self.assertNotIn("<b>XE9</b>", footer)

    # -- company name: table cell -------------------------------------------

    def test_company_is_escaped(self):
        self.assertNotIn('"XC2 onmouseover="', self.doc)
        self.assertIn(html.escape(HOSTILE_COMPANY, quote=True), self.doc)

    # -- data-text attribute (title+company+place, lowercased, escaped) -----

    def test_data_text_attribute_is_escaped(self):
        _, place = li_report._workplace(HOSTILE_LOCATION)
        raw = (HOSTILE_TITLE + " " + HOSTILE_COMPANY + " " + place).lower()
        self.assertIn(f"data-text='{html.escape(raw, quote=True)}'", self.doc)
        self.assertNotIn("data-text='<script", self.doc)

    # -- description: the drawer's <div class=jd> --------------------------

    def test_description_is_escaped(self):
        self.assertNotIn("<img", self.doc)
        self.assertIn("&lt;img", self.doc)
        self.assertNotIn("</script> too", self.doc)
        self.assertIn(html.escape(HOSTILE_DESCRIPTION, quote=True), self.doc)

    # -- location/place: the "Where" table cell (and inside data-text, above) --

    def test_location_place_is_escaped(self):
        _, place = li_report._workplace(HOSTILE_LOCATION)
        self.assertNotIn(place, self.doc)
        self.assertIn(f"</span> {html.escape(place, quote=True)}", self.doc)

    # -- urn shown verbatim in the drawer ------------------------------------

    def test_urn_in_drawer_is_escaped(self):
        full_urn = f"urn:li:fsd_jobPosting:{HOSTILE_URN_TAIL}"
        expected = html.escape(full_urn, quote=True)
        sub_divs = re.findall(r"<div class=sub>(.*?)</div>", self.doc)
        # Scoped to the actual <div class=sub> content, not a doc-wide
        # substring check: the link-href site below derives from this same
        # urn, so a doc-wide check could pass/fail for the wrong site's
        # reason. If this div were unescaped it would contain the raw
        # (differing) urn instead of `expected`.
        self.assertIn(expected, sub_divs)

    # -- link href, built from the SAME hostile urn via li_digest.job_link,
    #    which does NOT sanitise -- only quoting+escaping keeps it safe -----

    def test_link_href_is_escaped(self):
        full_urn = f"urn:li:fsd_jobPosting:{HOSTILE_URN_TAIL}"
        expected = html.escape(li_digest.job_link(full_urn), quote=True)
        # Extract by the delimiting quote characters actually present in
        # the doc, not by searching for a fixed prefix: if _esc were
        # removed here, the embedded raw "'" in HOSTILE_URN_TAIL would
        # close the attribute early and this regex would capture a
        # shorter, WRONG value ending at that quote -- which is exactly
        # the failure this is meant to catch.
        # The fixture renders two hostile rows (one with a description, one
        # without -- see setUpClass), so both external links are checked.
        second_urn = f"urn:li:fsd_jobPosting:{HOSTILE_URN_TAIL}9"
        expected_second = html.escape(li_digest.job_link(second_urn), quote=True)
        hrefs = re.findall(r"href='([^']*)'", self.doc)
        external = [h for h in hrefs if h.startswith("https://www.linkedin.com")]
        self.assertEqual(sorted(external), sorted([expected, expected_second]))

    # -- the local #fragment ids (title link + details id) -- the BLOCKING
    #    finding: constrained at the source by _urn_tail, AND quoted --------

    def test_fragment_ids_are_quoted_and_constrained(self):
        jid = li_report._urn_tail({"urn": f"urn:li:fsd_jobPosting:{HOSTILE_URN_TAIL}"})
        self.assertTrue(jid)
        self.assertRegex(jid, r"^[A-Za-z0-9_-]+$")
        frag_hrefs = re.findall(r"href='(#j[^']*)'", self.doc)
        frag_ids = re.findall(r"id='(j[^']*)'", self.doc)
        self.assertEqual(frag_hrefs, [f"#j{jid}"])
        self.assertEqual(frag_ids, [f"j{jid}"])
        # The historically-vulnerable UNQUOTED forms must never appear --
        # this is the quoting half of the two-part fix; the character-set
        # half is covered by TestUrnTail and the `assertRegex` above.
        self.assertNotIn("href=#j", self.doc)
        self.assertNotIn("<details id=j", self.doc)

    # -- lane chip, data-lanes attribute, lane <option> ----------------------
    # (all three driven by the archetype label; HOSTILE_CONFIG's is hostile)

    def test_lane_chip_is_escaped(self):
        self.assertNotIn("<b>XA7</b>", self.doc)
        self.assertIn(
            f"<span class=lane>{html.escape('<b>XA7</b>', quote=True)}</span>", self.doc,
        )

    def test_data_lanes_attribute_is_escaped(self):
        self.assertIn(f"data-lanes='{html.escape('<b>XA7</b>', quote=True)}'", self.doc)
        self.assertNotIn("data-lanes='<b>", self.doc)

    def test_lane_option_is_escaped(self):
        self.assertIn(f"<option>{html.escape('<b>XA7</b>', quote=True)}</option>", self.doc)

    # -- generated_at ---------------------------------------------------------

    def test_generated_at_is_escaped(self):
        self.assertNotIn("<script>XG6</script>", self.doc)
        self.assertIn(html.escape(HOSTILE_GENERATED_AT, quote=True), self.doc)


class TestWorkplaceParityWithLiDigest(unittest.TestCase):
    """li_report._workplace and li_digest._is_remote read the same marker
    convention with two separate regexes -- a 4-way classifier that drives
    CSS class names here, a boolean there. Promoting one into the other
    would push presentation into li_digest, so they stay separate; this
    locks them against drifting apart again, which is exactly the bug the
    case-sensitivity fix closed (li_report was case-sensitive, li_digest
    was not, so "(remote)" undercounted the Remote KPI).
    """

    CASES = [
        "Berlin, Germany (Remote)", "Berlin, Germany (remote)",
        "Berlin, Germany (REMOTE)", "Munich (Hybrid)", "Munich (hybrid)",
        "Hamburg (On-site)", "Hamburg (on-site)", "Germany", "",
    ]

    def test_remote_classification_agrees(self):
        for loc in self.CASES:
            with self.subTest(location=loc):
                self.assertEqual(
                    li_report._workplace(loc)[0] == "Remote",
                    li_digest._is_remote({"location": loc}),
                    f"{loc!r}: li_report says {li_report._workplace(loc)[0]}, "
                    f"li_digest._is_remote says {li_digest._is_remote({'location': loc})}",
                )


class TestUnsanitisableUrnAtRenderLevel(unittest.TestCase):
    """_urn_tail returning "" is guarded in render_html, but nothing drove
    the RENDERER with such a row: TestUrnTail exercises the helper in
    isolation, so removing either the `and jid` anchor guard or the id_attr
    guard left the whole suite green. Two rows whose urns sanitise to empty
    AND carry descriptions is the shape that breaks -- every anchor would
    point at a bare `#j` and both <details> would collide on the same id.
    """

    @classmethod
    def setUpClass(cls):
        cls.cfg = load_cfg(GOOD_CONFIG)
        rows = [
            cache_job("!!!", "Platform Engineer", description="first jd"),
            cache_job("@@@", "Platform Engineer", description="second jd"),
        ]
        enriched = li_digest.enrich_rows(rows, cls.cfg, date(2020, 1, 1))
        cls.doc = li_report.render_html(enriched, cls.cfg, 14, "smoke")

    def test_both_rows_still_render(self):
        self.assertEqual(self.doc.count("first jd"), 1)
        self.assertEqual(self.doc.count("second jd"), 1)

    def test_no_anchor_to_a_bare_fragment(self):
        self.assertNotIn("href='#j'", self.doc)
        self.assertNotIn("href=#j", self.doc)

    def test_no_colliding_empty_details_id(self):
        """Both fixture urns sanitise to empty, so the invariant is that
        NEITHER drawer carries an id -- asserted positively. A
        len(ids) == len(set(ids)) check would read as duplicate-detection
        while being vacuous here: healthy, ids is []; mutated, both ids are
        'j' and the assertNotIn above has already failed."""
        self.assertNotIn("id='j'", self.doc)
        ids = re.findall(r"<details[^>]*\bid='([^']*)'", self.doc)
        self.assertEqual(ids, [], "rows with unsanitisable urns must carry no details id")


class TestPostedCellIsEscaped(unittest.TestCase):
    """The 13th site: the posted-date cell -- unlike every other site
    above, this one cannot be exercised through the normal select_rows
    pipeline. bucket_of() only ever assigns bucket="in" after successfully
    parsing posted_at[:10] as a valid ISO date (pure digits and hyphens),
    so a posted_at that reaches _posted_cell via that real path can never
    itself carry an HTML metacharacter -- there is no hostile string that
    is both "parses as YYYY-MM-DD" and "contains '<'".

    render_html() is a public function that trusts its `rows` argument's
    "bucket" field, though -- it does not re-derive bucket from posted_at
    -- so a row built by hand (bypassing enrich_rows/bucket_of entirely)
    can still reach this call with a hostile value, and render_html must
    still escape it rather than assume every caller goes through the safe
    pipeline.
    """

    def test_posted_cell_is_escaped(self):
        cfg = load_cfg(GOOD_CONFIG)
        row = {
            "urn": "urn:li:fsd_jobPosting:999",
            "title": "Clean Title",
            "location": "Berlin",
            "company": {"name": "Clean Co"},
            "posted_at": "<b>2026</b>-08-04T00:00:00Z",
            "bucket": "in",
            "archetypes": "",
            "link": "https://www.linkedin.com/jobs/view/999/",
            "highlight": False,
        }
        doc = li_report.render_html([row], cfg, 14, "smoke")
        raw_prefix = row["posted_at"][:10]
        self.assertNotIn(raw_prefix, doc)
        self.assertIn(html.escape(raw_prefix, quote=True), doc)


class TestCli(ReportTempDir):

    def setUp(self):
        super().setUp()
        self.path = self.write_config(GOOD_CONFIG)
        self.out = io.StringIO()
        self.err = io.StringIO()

    def run_cli(self, *argv):
        return li_report.main(list(argv), out=self.out, err=self.err)

    def test_excluded_company_absent_from_the_job_table(self):
        """The footer legitimately names excluded companies for transparency
        ("Excluded: Initech") -- that is not the row this test guards
        against. What must never appear is an Initech *job row*."""
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["exclude_company"] = ["Initech"]
        path = self.write_config(data, "ex.json")
        self.write_cache([
            cache_job("1", "Platform Engineer", company="Initech"),
            cache_job("2", "Platform Engineer", company="Globex"),
        ])
        code = self.run_cli("--config", str(path), "--generated-at", "smoke")
        self.assertEqual(code, 0)
        doc = self.out.getvalue()
        tbody = doc.split("<tbody>", 1)[1].split("</tbody>", 1)[0]
        self.assertNotIn("Initech", tbody)
        self.assertIn("Globex", tbody)

    def test_old_bucket_jobs_excluded_from_cli_output(self):
        self.write_cache([
            cache_job("1", "Platform Engineer", posted="2000-01-01T00:00:00Z"),
        ])
        code = self.run_cli("--config", str(self.path), "--generated-at", "smoke")
        self.assertEqual(code, 0)
        self.assertNotIn("Platform Engineer", self.out.getvalue())

    def test_undated_job_appears_in_cli_output(self):
        self.write_cache([
            cache_job("1", "Platform Engineer", posted=li_digest.ZERO_DATE),
        ])
        code = self.run_cli("--config", str(self.path), "--generated-at", "smoke")
        self.assertEqual(code, 0)
        self.assertIn("Platform Engineer", self.out.getvalue())
        self.assertNotIn("0001-01-01", self.out.getvalue())

    def test_empty_cache_exits_clean_with_valid_html_on_stdout(self):
        self.write_cache([])
        code = self.run_cli("--config", str(self.path), "--generated-at", "smoke")
        self.assertEqual(code, 0)
        self.assertTrue(self.out.getvalue().startswith("<!doctype html>"))

    def test_out_flag_writes_a_file_and_stdout_stays_empty(self):
        self.write_cache([cache_job("1", "Platform Engineer")])
        out_path = self.tmp / "report.html"
        code = self.run_cli(
            "--config", str(self.path), "--generated-at", "smoke", "--out", str(out_path),
        )
        self.assertEqual(code, 0)
        self.assertEqual(self.out.getvalue(), "")
        self.assertTrue(out_path.exists())
        self.assertIn("Platform Engineer", out_path.read_text(encoding="utf-8"))
        self.assertIn("li-report: wrote", self.err.getvalue())

    def test_omitting_out_writes_to_stdout(self):
        self.write_cache([cache_job("1", "Platform Engineer")])
        code = self.run_cli("--config", str(self.path), "--generated-at", "smoke")
        self.assertEqual(code, 0)
        self.assertIn("Platform Engineer", self.out.getvalue())

    def test_out_parent_directory_missing_exits_2(self):
        """A mistyped --out path is the likeliest error on this flag. Left
        uncaught, write_text's FileNotFoundError escapes the try/except
        ConfigError and exits 1 -- outside the documented 0/2 contract."""
        self.write_cache([cache_job("1", "Platform Engineer")])
        bad_out = self.tmp / "nope" / "r.html"
        code = self.run_cli(
            "--config", str(self.path), "--generated-at", "smoke", "--out", str(bad_out),
        )
        self.assertEqual(code, 2)
        self.assertIn("li-report:", self.err.getvalue())
        self.assertFalse(bad_out.exists())

    def test_out_path_is_a_directory_exits_2(self):
        self.write_cache([cache_job("1", "Platform Engineer")])
        a_dir = self.tmp / "adir"
        a_dir.mkdir()
        code = self.run_cli(
            "--config", str(self.path), "--generated-at", "smoke", "--out", str(a_dir),
        )
        self.assertEqual(code, 2)
        self.assertIn("li-report:", self.err.getvalue())

    def test_mixed_posted_at_types_does_not_crash_and_exits_0(self):
        second = dict(cache_job("2", "Platform Engineer"), posted_at=20260804)
        self.write_cache([
            cache_job("1", "Platform Engineer", posted="2026-08-01T00:00:00Z"),
            second,
        ])
        code = self.run_cli("--config", str(self.path), "--generated-at", "smoke")
        self.assertEqual(code, 0)

    def test_negative_window_exits_2(self):
        self.write_cache([cache_job("1", "Platform Engineer")])
        code = self.run_cli(
            "--config", str(self.path), "--generated-at", "smoke", "--window", "-1",
        )
        self.assertEqual(code, 2)
        self.assertIn("--window", self.err.getvalue())
        self.assertEqual(self.out.getvalue(), "")

    def test_missing_config_exits_2(self):
        code = self.run_cli("--config", str(self.tmp / "nope.json"), "--generated-at", "smoke")
        self.assertEqual(code, 2)

    def test_missing_cache_exits_2(self):
        # No write_cache() call -- the cache file never gets created.
        code = self.run_cli("--config", str(self.path), "--generated-at", "smoke")
        self.assertEqual(code, 2)
        self.assertIn("cache", self.err.getvalue())

    def test_unknown_flag_exits_2_without_raising(self):
        code = self.run_cli("--config", str(self.path), "--bogus")
        self.assertEqual(code, 2)

    def test_help_returns_0_and_writes_to_injected_out(self):
        code = self.run_cli("--help")
        self.assertEqual(code, 0)
        self.assertIn("usage", self.out.getvalue().lower())

    def test_generated_at_defaults_when_omitted(self):
        self.write_cache([cache_job("1", "Platform Engineer")])
        code = self.run_cli("--config", str(self.path))
        self.assertEqual(code, 0)
        self.assertIn("UTC", self.out.getvalue())


class TestMainAsScript(unittest.TestCase):
    """Guards the __main__/_CSS/_JS ordering fix: `unittest discover` only
    ever imports this module, so a regression that moves
    `if __name__ == "__main__"` back above the CSS/JS constants reproduces
    `NameError: name '_CSS' is not defined` on every real invocation while
    the rest of this test suite stays green (import already finished
    executing the whole module before any test calls main()). Actually
    execve'ing the file is the only way to catch that class of bug -- this
    is exactly how the real bug was first found, via the live smoke test,
    not via `unittest discover`.
    """

    def test_file_is_executable(self):
        """SKILL.md tells you to symlink this into ~/.local/bin, so the mode
        is part of the interface: at 100644 the documented first command
        exits 126 (Permission denied). The two subprocess tests below pass
        `sys.executable` explicitly and so bypass the shebang path
        entirely -- neither would notice the bit being lost."""
        self.assertTrue(
            os.access(li_report.__file__, os.X_OK),
            f"{li_report.__file__} must be executable -- SKILL.md symlinks it into ~/.local/bin",
        )

    def test_runs_as_a_script_and_prints_html(self):
        with tempfile.TemporaryDirectory() as home:
            home = Path(home)
            cache_dir = home / ".config" / "li-assist" / "cache"
            cache_dir.mkdir(parents=True)
            (cache_dir / "jobs.jsonl").write_text(
                json.dumps(cache_job("1", "Platform Engineer")) + "\n", encoding="utf-8"
            )
            cfg_path = home / "archetypes.json"
            cfg_path.write_text(json.dumps(GOOD_CONFIG), encoding="utf-8")

            proc = subprocess.run(
                [sys.executable, li_report.__file__,
                 "--config", str(cfg_path), "--generated-at", "smoke"],
                capture_output=True, text=True,
                env={**os.environ, "HOME": str(home)},
            )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(proc.stdout.startswith("<!doctype html>"))
        self.assertIn("Platform Engineer", proc.stdout)


class TestBrokenPipe(unittest.TestCase):
    """`li-report | head` closes the read end early; SKILL.md advertises
    piping stdout. Writing to a closed pipe raises BrokenPipeError, and an
    uncaught one produces a traceback on exit -- this actually pipes to
    `head` in a real shell and asserts there is no traceback."""

    def test_no_traceback_when_stdout_closes_early(self):
        with tempfile.TemporaryDirectory() as home:
            home = Path(home)
            cache_dir = home / ".config" / "li-assist" / "cache"
            cache_dir.mkdir(parents=True)
            # Enough rows (each with a sizeable description) that the write
            # genuinely does not fit in one pipe buffer flush before `head`
            # closes its end.
            rows = [
                cache_job(str(i), f"Platform Engineer {i}", description="x" * 2000)
                for i in range(300)
            ]
            (cache_dir / "jobs.jsonl").write_text(
                "\n".join(json.dumps(r) for r in rows) + "\n", encoding="utf-8"
            )
            cfg_path = home / "archetypes.json"
            cfg_path.write_text(json.dumps(GOOD_CONFIG), encoding="utf-8")

            script = sys.executable + " " + li_report.__file__
            proc = subprocess.run(
                f'{script} --config "{cfg_path}" --generated-at smoke | head -c 1 >/dev/null',
                shell=True, capture_output=True, text=True,
                env={**os.environ, "HOME": str(home)},
            )
        self.assertNotIn("Traceback", proc.stderr)
        self.assertNotIn("BrokenPipeError", proc.stderr)


class TestDocsMatchImplementation(unittest.TestCase):
    """SKILL.md must not document flags neither tool's parser has.

    Generalized here (moved from test_li_digest.py, see the pointer comment
    left there) because covering li-report's flags means importing
    li_report, and test_li_digest.py has no other reason to know that
    module exists.
    """

    PARSERS = {
        "li-digest": li_digest.build_parser,
        "li-report": li_report.build_parser,
    }

    # Both docs, whole-file. The digest reference was split out of SKILL.md
    # into DIGEST.md behind a pointer, and SKILL.md still carries li-digest
    # and li-report lines in Recipes -- so scanning one file, or slicing to a
    # single heading, silently stops covering most of what it is meant to
    # guard. Whole-file over both is also one less thing to keep in sync than
    # a heading name.
    DOC_NAMES = ("SKILL.md", "DIGEST.md")

    def test_documented_flags_all_exist(self):
        skill_dir = Path(__file__).resolve().parents[2]
        fences = []
        for name in self.DOC_NAMES:
            doc = skill_dir / name
            self.assertTrue(doc.is_file(), f"{name} is missing from {skill_dir}")
            fences += re.findall(
                r"```(?:[a-zA-Z]*)\n(.*?)\n```",
                doc.read_text(encoding="utf-8"),
                re.DOTALL,
            )

        # Attribute per LINE, not per fence. A single block legitimately mixes
        # both commands (a recipe that digests then reports), and charging the
        # whole block to whichever command happens to be on line one blames
        # li-digest for li-report's --out.
        lines = [line.strip() for block in fences for line in block.splitlines()]
        for prefix, get_parser in self.PARSERS.items():
            documented = set()
            for line in lines:
                if line.startswith(prefix):
                    documented.update(re.findall(r"(?<![\w-])--[a-z][a-z-]+", line))
            self.assertTrue(
                documented,
                f"found no flags in any {prefix} command block — check the "
                "heading split and the fence filter",
            )
            parser_flags = set()
            for action in get_parser()._actions:
                parser_flags.update(
                    opt for opt in action.option_strings if opt.startswith("--")
                )
            self.assertEqual(
                documented - parser_flags, set(),
                f"{prefix}: the skill docs name a flag its parser does not have",
            )


if __name__ == "__main__":
    unittest.main()
