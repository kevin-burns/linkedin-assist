"""Stdlib-only tests for li_digest. No network, no LinkedIn session."""

import datetime as _datetime_module
import json
import re
import sys
import tempfile
import unittest
from datetime import date, datetime, timezone
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import li_digest  # noqa: E402


class _FixedUTCNow(_datetime_module.datetime):
    """Stands in for li_digest's `datetime` so `datetime.now(timezone.utc)`
    returns a fixed instant, deterministically, regardless of the real
    wall clock (must still pass in 2030). Represents an evening run at a
    negative UTC offset: UTC is already past midnight into the next day."""

    _FIXED_UTC_NOW = _datetime_module.datetime(
        2026, 8, 5, 0, 10, 0, tzinfo=_datetime_module.timezone.utc
    )

    @classmethod
    def now(cls, tz=None):
        return cls._FIXED_UTC_NOW


class _FixedLocalToday(_datetime_module.date):
    """Stands in for li_digest's `date` so `date.today()` returns a fixed
    LOCAL date one day behind `_FixedUTCNow` -- the same negative-UTC-offset
    evening this whole bug is about. Only a REVERTED read_last_run (falling
    back to `date.today()` instead of the UTC instant) ever consults this."""

    @classmethod
    def today(cls):
        return cls(2026, 8, 4)


GOOD_CONFIG = {
    "defaults": {"location": "Germany", "limit": 50, "exclude_title": ["recruiter"]},
    "archetypes": [
        {"name": "platform", "label": "Platform",
         "query": '"platform engineer"', "match": "platform|terraform"},
        {"name": "em", "label": "EM",
         "query": '"engineering manager"', "match": "engineering manager|tech lead"},
    ],
}


class ConfigTempDir(unittest.TestCase):
    """Base class giving each test an isolated config directory."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)
        self.addCleanup(self._tmp.cleanup)

    def write_config(self, data, name="archetypes.json"):
        path = self.tmp / name
        path.write_text(json.dumps(data), encoding="utf-8")
        return path


class TestLoadConfig(ConfigTempDir):

    def test_loads_archetypes_and_applies_defaults(self):
        cfg = li_digest.load_config(self.write_config(GOOD_CONFIG))
        self.assertEqual(len(cfg.archetypes), 2)
        first = cfg.archetypes[0]
        self.assertEqual(first.name, "platform")
        self.assertEqual(first.label, "Platform")
        self.assertEqual(first.query, '"platform engineer"')
        self.assertEqual(first.location, "Germany")
        self.assertEqual(first.limit, 50)
        self.assertEqual(cfg.exclude_titles, ("recruiter",))
        self.assertEqual(cfg.exclude_companies, ())

    def test_per_archetype_location_overrides_the_default(self):
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["archetypes"][1]["location"] = "European Union"
        cfg = li_digest.load_config(self.write_config(data))
        self.assertEqual(cfg.archetypes[0].location, "Germany")
        self.assertEqual(cfg.archetypes[1].location, "European Union")

    def test_compiled_pattern_is_case_insensitive(self):
        cfg = li_digest.load_config(self.write_config(GOOD_CONFIG))
        self.assertTrue(cfg.archetypes[0].pattern.search("Senior PLATFORM Engineer"))

    def test_rejects_missing_required_field(self):
        data = {"archetypes": [{"name": "x", "label": "X", "query": "q"}]}
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config(data))
        self.assertIn("match", str(ctx.exception))

    def test_rejects_empty_string_required_field(self):
        """"match": "" used to pass isinstance(..., str) and compile into a
        regex that matches everything, silently labelling every job under
        this archetype."""
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["archetypes"][0]["match"] = ""
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config(data))
        self.assertIn("missing or empty", str(ctx.exception))
        self.assertIn("match", str(ctx.exception))

    def test_rejects_invalid_regex(self):
        data = {"archetypes": [{"name": "x", "label": "X", "query": "q", "match": "a[b"}]}
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config(data))
        self.assertIn("invalid match regex", str(ctx.exception))

    def test_rejects_empty_archetype_list(self):
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config({"archetypes": []}))
        self.assertIn("no archetypes", str(ctx.exception))

    def test_rejects_duplicate_names(self):
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["archetypes"][1]["name"] = "platform"
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config(data))
        self.assertIn("duplicate", str(ctx.exception))

    def test_rejects_non_numeric_limit(self):
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["archetypes"][0]["limit"] = "fifty"
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config(data))
        self.assertIn("invalid limit", str(ctx.exception))

    def test_rejects_non_dict_defaults(self):
        data = {"defaults": "oops", "archetypes": GOOD_CONFIG["archetypes"]}
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.write_config(data))
        self.assertIn("defaults", str(ctx.exception))

    def test_rejects_missing_file(self):
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(self.tmp / "nope.json")
        self.assertIn("not readable", str(ctx.exception))

    def test_rejects_malformed_json(self):
        path = self.tmp / "junk.json"
        path.write_text("not json", encoding="utf-8")
        with self.assertRaises(li_digest.ConfigError) as ctx:
            li_digest.load_config(path)
        self.assertIn("not valid JSON", str(ctx.exception))

    def test_example_config_shipped_in_the_repo_is_valid(self):
        example = Path(__file__).resolve().parent.parent / "archetypes.example.json"
        cfg = li_digest.load_config(example)
        self.assertEqual(len(cfg.archetypes), 5)


class TestHighlight(ConfigTempDir):
    """defaults.highlight: plain terms (not regexes) that star a matching
    row so a strongly-matching operator differentiator isn't invisible in a
    long table of archetype-only matches."""

    def config_with_highlight(self, terms):
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["defaults"]["highlight"] = terms
        return li_digest.load_config(self.write_config(data))

    def test_highlight_pattern_is_none_when_absent(self):
        cfg = li_digest.load_config(self.write_config(GOOD_CONFIG))
        self.assertIsNone(cfg.highlight_pattern)

    def test_highlight_pattern_is_none_when_empty_list(self):
        self.assertIsNone(self.config_with_highlight([]).highlight_pattern)

    def test_highlight_pattern_is_none_when_not_a_list(self):
        self.assertIsNone(self.config_with_highlight("terraform").highlight_pattern)

    def test_highlight_pattern_matches_case_insensitively(self):
        cfg = self.config_with_highlight(["terraform"])
        self.assertTrue(cfg.highlight_pattern.search("Senior TERRAFORM Engineer"))

    def test_metacharacter_terms_are_literal_and_do_not_raise(self):
        """"c++" as a raw regex is invalid ("multiple repeat") and would
        raise re.error at compile time; escaped, it is a literal three-char
        match instead."""
        cfg = self.config_with_highlight(["c++", ".net"])
        self.assertIsNotNone(cfg.highlight_pattern)
        rows = li_digest.enrich_rows(
            [job("1", "Senior C++ Developer"), job("2", "Xnet Support"),
             job("3", ".NET Core Developer")],
            cfg, date(2026, 7, 21),
        )
        by_id = {r["urn"].rsplit(":", 1)[-1]: r["highlight"] for r in rows}
        self.assertTrue(by_id["1"])
        self.assertFalse(by_id["2"], "unescaped '.' would wrongly match 'Xnet'")
        self.assertTrue(by_id["3"])

    def test_enrich_rows_sets_highlight_true_and_false(self):
        cfg = self.config_with_highlight(["terraform"])
        rows = li_digest.enrich_rows(
            [job("1", "Terraform Engineer"), job("2", "Support Analyst")],
            cfg, date(2026, 7, 21),
        )
        by_id = {r["urn"].rsplit(":", 1)[-1]: r["highlight"] for r in rows}
        self.assertTrue(by_id["1"])
        self.assertFalse(by_id["2"])

    def test_missing_empty_non_list_highlight_is_a_clean_noop(self):
        for terms in (None, [], "terraform", 42):
            with self.subTest(terms=terms):
                data = json.loads(json.dumps(GOOD_CONFIG))
                if terms is not None:
                    data["defaults"]["highlight"] = terms
                cfg = li_digest.load_config(self.write_config(data, f"cfg-{terms}.json"))
                rows = li_digest.enrich_rows(
                    [job("1", "Terraform Engineer")], cfg, date(2026, 7, 21)
                )
                self.assertFalse(rows[0]["highlight"])

    def test_non_string_title_does_not_crash_highlighting(self):
        cfg = self.config_with_highlight(["terraform"])
        rows = li_digest.enrich_rows(
            [{"urn": "urn:li:fsd_jobPosting:9", "title": 42, "location": "Remote",
              "company": "Acme", "posted_at": "2026-08-04T00:00:00Z"}],
            cfg, date(2026, 7, 21),
        )
        self.assertFalse(rows[0]["highlight"])

    def test_json_output_carries_the_highlight_boolean(self):
        cfg = self.config_with_highlight(["terraform"])
        rows = li_digest.enrich_rows(
            [job("1", "Terraform Engineer")], cfg, date(2026, 7, 21)
        )
        self.assertIs(json.loads(json.dumps(rows))[0]["highlight"], True)

    def test_table_stars_a_highlighted_title(self):
        cfg = self.config_with_highlight(["terraform"])
        rows = li_digest.enrich_rows(
            [job("1", "Terraform Engineer")], cfg, date(2026, 7, 21)
        )
        table = li_digest.render_table(rows)
        self.assertIn("★ Terraform Engineer", table)

    def test_table_alignment_holds_with_mixed_starred_rows(self):
        cfg = self.config_with_highlight(["terraform"])
        rows = li_digest.enrich_rows(
            [job("1", "Terraform Engineer", "Acme"), job("2", "Support Analyst", "Acme")],
            cfg, date(2026, 7, 21),
        )
        table = li_digest.render_table(rows)
        data_lines = [
            line for line in table.splitlines()
            if "Terraform Engineer" in line or "Support Analyst" in line
        ]
        self.assertEqual(len(data_lines), 2)
        positions = {line.index("Germany") for line in data_lines}
        self.assertEqual(len(positions), 1, "Location column must still line up")


class TestPureHelpers(ConfigTempDir):

    def archetypes(self):
        return li_digest.load_config(self.write_config(GOOD_CONFIG)).archetypes

    def test_job_link_uses_the_urn_tail(self):
        self.assertEqual(
            li_digest.job_link("urn:li:fsd_jobPosting:4431723620"),
            "https://www.linkedin.com/jobs/view/4431723620/",
        )

    def test_job_link_accepts_a_bare_id(self):
        self.assertEqual(
            li_digest.job_link("4431723620"),
            "https://www.linkedin.com/jobs/view/4431723620/",
        )

    def test_cutoff_date_counts_back_from_today(self):
        self.assertEqual(
            li_digest.cutoff_date(14, today=date(2026, 8, 4)), date(2026, 7, 21)
        )

    def test_zero_value_date_is_undated_not_dropped(self):
        self.assertEqual(
            li_digest.bucket_of(li_digest.ZERO_DATE, date(2026, 7, 21)), "undated"
        )

    def test_missing_empty_and_unparseable_dates_are_undated(self):
        cutoff = date(2026, 7, 21)
        for value in (None, "", "not-a-date", "2026-13-45T00:00:00Z"):
            with self.subTest(value=value):
                self.assertEqual(li_digest.bucket_of(value, cutoff), "undated")

    def test_in_window_boundary_is_inclusive(self):
        cutoff = date(2026, 7, 21)
        self.assertEqual(li_digest.bucket_of("2026-08-04T00:00:00Z", cutoff), "in")
        self.assertEqual(li_digest.bucket_of("2026-07-21T00:00:00Z", cutoff), "in")
        self.assertEqual(li_digest.bucket_of("2026-07-20T23:59:59Z", cutoff), "old")

    def test_bucket_of_last_run_defaults_to_none_and_is_unaffected(self):
        """Every pre-existing call site (two positional args) must keep
        working unchanged."""
        cutoff = date(2026, 7, 21)
        self.assertEqual(li_digest.bucket_of("2026-08-04T00:00:00Z", cutoff), "in")

    def test_bucket_of_marks_fresh_when_posted_on_or_after_last_run(self):
        cutoff = date(2026, 7, 21)
        last_run = date(2026, 8, 3)
        self.assertEqual(
            li_digest.bucket_of("2026-08-04T00:00:00Z", cutoff, last_run), "fresh"
        )
        self.assertEqual(
            li_digest.bucket_of("2026-08-03T00:00:00Z", cutoff, last_run), "fresh"
        )

    def test_bucket_of_falls_back_to_in_old_when_posted_before_last_run(self):
        cutoff = date(2026, 7, 21)
        last_run = date(2026, 8, 3)
        self.assertEqual(
            li_digest.bucket_of("2026-08-02T00:00:00Z", cutoff, last_run), "in"
        )
        self.assertEqual(
            li_digest.bucket_of("2026-07-01T00:00:00Z", cutoff, last_run), "old"
        )

    def test_bucket_of_undated_stays_undated_even_with_a_last_run(self):
        cutoff = date(2026, 7, 21)
        last_run = date(2026, 8, 3)
        self.assertEqual(
            li_digest.bucket_of(li_digest.ZERO_DATE, cutoff, last_run), "undated"
        )

    def test_last_run_marker_mirrors_seed_marker(self):
        config_path = self.tmp / "archetypes.json"
        self.assertEqual(
            li_digest.last_run_marker(config_path), self.tmp / ".digest-lastrun"
        )
        self.assertEqual(
            li_digest.last_run_marker(config_path).parent,
            li_digest.seed_marker(config_path).parent,
        )

    def test_read_last_run_missing_file_is_none(self):
        self.assertIsNone(li_digest.read_last_run(self.tmp / "nope"))

    def test_read_last_run_empty_file_is_none(self):
        path = self.tmp / "empty"
        path.write_text("", encoding="utf-8")
        self.assertIsNone(li_digest.read_last_run(path))

    def test_read_last_run_malformed_content_is_none(self):
        path = self.tmp / "junk"
        path.write_text("not a timestamp", encoding="utf-8")
        self.assertIsNone(li_digest.read_last_run(path))

    def test_read_last_run_future_timestamp_is_none(self):
        """A future timestamp must not silently be treated as a valid
        cutoff -- degrade to None like any other malformed marker."""
        path = self.tmp / "future"
        path.write_text("9999-01-01T00:00:00Z", encoding="utf-8")
        self.assertIsNone(li_digest.read_last_run(path))

    def test_read_last_run_valid_timestamp_returns_its_date(self):
        path = self.tmp / "good"
        path.write_text("2026-08-04T09:30:00Z", encoding="utf-8")
        self.assertEqual(li_digest.read_last_run(path), date(2026, 8, 4))

    def test_read_last_run_tomorrow_relative_to_injected_today_is_none(self):
        """The near-future boundary, not just the absurd `9999` case: a
        stamp one day ahead of `today` must still degrade to None.

        This only exercises the `today`-KWARG seam, not the UTC-vs-local
        default itself -- it is clock-dependent by construction (a
        hard-coded 2026-08-04/05 pair) and would still pass even if the
        production default silently reverted from UTC to local `date.today()`.
        See test_read_last_run_default_uses_utc_now_not_local_today below
        for the test that actually pins the UTC default, deterministically,
        with no reliance on the real clock."""
        path = self.tmp / "tomorrow"
        path.write_text("2026-08-05T00:00:00Z", encoding="utf-8")
        self.assertIsNone(li_digest.read_last_run(path, today=date(2026, 8, 4)))

    def test_read_last_run_today_relative_to_injected_today_is_valid(self):
        """The boundary's other side: a stamp dated exactly `today` (the
        ordinary case -- written earlier today, read back later today) must
        NOT be treated as future."""
        path = self.tmp / "same-day"
        path.write_text("2026-08-04T23:00:00Z", encoding="utf-8")
        self.assertEqual(
            li_digest.read_last_run(path, today=date(2026, 8, 4)), date(2026, 8, 4)
        )

    def test_read_last_run_default_uses_utc_now_not_local_today(self):
        """Pins the actual UTC-vs-local fix, distinct from the `today`-kwarg
        boundary tests above (those only exercise the kwarg seam and are
        satisfied whether the fallback is UTC or local). Called with NO
        `today` argument -- the real production call shape -- so this is
        the one that must fail if `today or datetime.now(timezone.utc)` is
        ever reverted to `today or date.today()`.

        `datetime.now(timezone.utc)` is pinned to 2026-08-05T00:10:00Z;
        `date.today()` is pinned to the LOCAL 2026-08-04 a REVERTED
        implementation would fall back to -- one day behind, the
        negative-UTC-offset evening scenario from the bug report. Neither
        pin touches the real clock, so this holds in 2030 too.
        """
        with mock.patch("li_digest.datetime", _FixedUTCNow), \
             mock.patch("li_digest.date", _FixedLocalToday):
            # Dated exactly at the (mocked) UTC "now" date: valid under the
            # correct UTC default, but "future" under a reverted LOCAL
            # default (2026-08-05 > 2026-08-04) -- this is the case that
            # actually discriminates the fix from the bug.
            utc_today_marker = self.tmp / "utc-today"
            utc_today_marker.write_text("2026-08-05T00:05:00Z", encoding="utf-8")
            self.assertEqual(
                li_digest.read_last_run(utc_today_marker), date(2026, 8, 5)
            )

            # Dated one day past even the mocked UTC "now": future under
            # either default. Doesn't discriminate the fix from the bug on
            # its own, but confirms the ordinary future-guard still holds
            # under the patched clock.
            utc_tomorrow_marker = self.tmp / "utc-tomorrow"
            utc_tomorrow_marker.write_text("2026-08-06T00:05:00Z", encoding="utf-8")
            self.assertIsNone(li_digest.read_last_run(utc_tomorrow_marker))

    def test_bucket_of_fresh_wins_over_out_of_window(self):
        """Documented, not changed: the fresh check runs before the cutoff
        check, so when `last_run` is OLDER than the window (the gap since
        the operator's last run exceeds --window), a posting outside the
        window can still be promoted to "fresh" if it's on/after last_run.
        No row is lost either way -- only which bucket it lands in -- so
        this interaction is intentional. Untested interactions are this
        project's entire defect history, hence pinning it here."""
        cutoff = date(2026, 7, 21)     # e.g. --window 14 from "today" 2026-08-04
        last_run = date(2026, 7, 10)   # last run was 25 days ago -- older than the window
        posted = date(2026, 7, 15)     # outside the window, but on/after last_run
        self.assertLess(posted, cutoff, "sanity: this WOULD be 'old' without last_run")
        self.assertEqual(
            li_digest.bucket_of(f"{posted.isoformat()}T00:00:00Z", cutoff, last_run),
            "fresh",
        )

    def test_labels_every_matching_archetype(self):
        self.assertEqual(
            li_digest.labels_for("Platform Engineering Manager", "EM", self.archetypes()),
            "Platform, EM",
        )

    def test_labels_a_single_match(self):
        self.assertEqual(
            li_digest.labels_for("Senior Terraform Consultant", "Platform", self.archetypes()),
            "Platform",
        )

    def test_falls_back_to_the_originating_archetype(self):
        self.assertEqual(
            li_digest.labels_for("Chief Widget Officer", "EM", self.archetypes()), "EM"
        )

    def test_labelling_is_case_insensitive(self):
        self.assertEqual(
            li_digest.labels_for("ENGINEERING MANAGER (F/M/D)", "EM", self.archetypes()), "EM"
        )

    def test_every_shipped_archetype_match_fires_on_a_plausible_title(self):
        """Guards the query/match drift risk called out in spec section 7.1."""
        example = Path(__file__).resolve().parent.parent / "archetypes.example.json"
        archetypes = li_digest.load_config(example).archetypes
        expected = {
            "Platform Engineer": "Platform",
            "Forward Deployed Engineer": "FDE",
            "Engineering Manager": "EM",
            "Solution Architect": "Architect",
            "KI Engineer (m/w/d)": "AI",
            "Generative AI Platform Engineer": "AI",
        }
        for title, label in expected.items():
            with self.subTest(title=title):
                self.assertIn(label, li_digest.labels_for(title, "", archetypes))


class FakeProc:
    def __init__(self, stdout="[]", stderr="", returncode=0):
        self.stdout = stdout
        self.stderr = stderr
        self.returncode = returncode


class FakeRunner:
    """Stands in for subprocess.run. Keyed on the archetype's query string."""

    def __init__(self, by_query=None, default=None):
        self.by_query = by_query or {}
        self.default = default or FakeProc()
        self.calls = []

    def __call__(self, cmd, capture_output=True, text=True, check=False):
        self.calls.append(cmd)
        for needle, proc in self.by_query.items():
            if any(needle in part for part in cmd):
                return proc
        return self.default


def job(urn, title, company="Acme", posted="2026-08-04T00:00:00Z",
        location="Germany (Remote)"):
    return {"urn": f"urn:li:fsd_jobPosting:{urn}", "title": title,
            "location": location, "company": {"urn": "", "name": company},
            "posted_at": posted}


PLATFORM_JOBS = [job("111", "Platform Engineer"),
                 job("222", "Engineering Manager, Platform", "Globex", "2026-08-03T00:00:00Z")]
EM_JOBS = [job("222", "Engineering Manager, Platform", "Globex", "2026-08-03T00:00:00Z"),
           job("333", "Tech Lead", "Initech", li_digest.ZERO_DATE)]


class TestCollect(ConfigTempDir):

    def setUp(self):
        super().setUp()
        self.cfg = li_digest.load_config(self.write_config(GOOD_CONFIG))
        self.logs = []

    def runner(self, **kwargs):
        return FakeRunner(by_query={
            "platform engineer": FakeProc(json.dumps(PLATFORM_JOBS)),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        }, **kwargs)

    def test_builds_the_expected_sweep_command(self):
        cmd = li_digest.build_sweep_cmd(self.cfg.archetypes[0], self.cfg)
        self.assertEqual(cmd[:3], ["li-assist", "jobs", "sweep"])
        self.assertIn('"platform engineer"', cmd)
        self.assertIn("--location", cmd)
        self.assertIn("Germany", cmd)
        self.assertIn("--exclude-title", cmd)
        self.assertIn("recruiter", cmd)
        self.assertNotIn("--enrich", cmd)

    def test_omits_location_when_blank(self):
        data = json.loads(json.dumps(GOOD_CONFIG))
        del data["defaults"]["location"]
        cfg = li_digest.load_config(self.write_config(data, "b.json"))
        self.assertNotIn("--location", li_digest.build_sweep_cmd(cfg.archetypes[0], cfg))

    def test_merges_and_dedups_by_urn(self):
        rows, failed = li_digest.collect(self.cfg, run=self.runner(), log=self.logs.append)
        self.assertEqual(failed, [])
        self.assertEqual(sorted(r["urn"].rsplit(":", 1)[-1] for r in rows),
                         ["111", "222", "333"])

    def test_forwards_warning_lines_from_a_successful_sweep(self):
        """On a SUCCESSFUL sweep the child's stderr used to be discarded
        entirely, including a cache-open warning that means "this run
        cached nothing" while still exiting 0. In that state --seed
        succeeds, writes the marker, and every later run silently reports
        everything as new forever. The exact real wire string, from
        cmd/li-assist/jobs.go."""
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc(
                json.dumps(PLATFORM_JOBS),
                "WARNING: could not open job cache: permission denied "
                "-- running without cache",
                0,
            ),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        li_digest.collect(self.cfg, run=runner, log=self.logs.append)
        self.assertTrue(
            any("WARNING: could not open job cache" in line for line in self.logs)
        )

    def test_first_sighting_wins_for_origin(self):
        rows, _ = li_digest.collect(self.cfg, run=self.runner(), log=self.logs.append)
        by_id = {r["urn"].rsplit(":", 1)[-1]: r for r in rows}
        self.assertEqual(by_id["111"]["origin"], "Platform")
        self.assertEqual(by_id["222"]["origin"], "Platform")
        self.assertEqual(by_id["333"]["origin"], "EM")

    def test_runs_one_sweep_per_archetype_sequentially(self):
        """Assert the recorded CALL ORDER, not just the count -- a count-only
        assertion would stay green even under a refactor to concurrent
        dispatch, which the sweep loop must never become (a burst of
        automated requests is exactly what gets a LinkedIn account
        flagged)."""
        runner = self.runner()
        li_digest.collect(self.cfg, run=runner, log=self.logs.append)
        self.assertEqual(len(runner.calls), 2)
        self.assertIn('"platform engineer"', runner.calls[0])
        self.assertIn('"engineering manager"', runner.calls[1])

    def test_a_failing_archetype_does_not_lose_the_others(self):
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc("", "boom", 1),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        rows, failed = li_digest.collect(self.cfg, run=runner, log=self.logs.append)
        self.assertEqual(failed, ["platform"])
        self.assertEqual(len(rows), 2)

    def test_unparseable_output_is_a_failure_not_a_crash(self):
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc("<html>not json</html>"),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        rows, failed = li_digest.collect(self.cfg, run=runner, log=self.logs.append)
        self.assertEqual(failed, ["platform"])
        self.assertEqual(len(rows), 2)

    # These are the REAL strings li-assist emits on a dead session, taken
    # from the Go source (not imagined): the local cookie-presence check in
    # cmd/li-assist/jobs.go, and the voyager-401 chain that wraps
    # domain.ErrAuth ("authentication failed") through
    # internal/voyager/client.go -> internal/voyager/jobs.go ("voyager jobs
    # search get: ...") -> internal/usecase/sweep_jobs.go ("sweep search:
    # ...") -> cmd/li-assist/jobs.go's sweep command ("jobs sweep: ...").
    DEAD_SESSION_STDERRS = (
        "not logged in -- run 'li-assist auth login' to open a browser "
        "window and sign in; completing 2FA is normal and expected; tick "
        '"Keep me logged in" so the session persists across commands',
        "jobs sweep: sweep search: voyager jobs search get: "
        "authentication failed: re-run li-assist auth login",
    )

    def test_a_dead_session_aborts_instead_of_burning_every_archetype(self):
        for stderr in self.DEAD_SESSION_STDERRS:
            with self.subTest(stderr=stderr):
                runner = FakeRunner(default=FakeProc("", stderr, 1))
                with self.assertRaises(li_digest.AuthError):
                    li_digest.collect(self.cfg, run=runner, log=self.logs.append)
                self.assertEqual(len(runner.calls), 1)

    # Real 429 / daily-cap chains, same provenance as above: the voyager-429
    # branch wraps domain.ErrRateLimit ("rate limited"), and the daily-cap
    # branch wraps internal/ratelimit.ErrDailyCapExceeded ("daily cap
    # exceeded") inside client.go's "rate limit: %w".
    RATE_LIMIT_STDERRS = (
        "jobs sweep: sweep search: voyager jobs search get: "
        "rate limited: voyager /voyager/api/voyagerJobsDashJobCards",
        "jobs sweep: sweep search: voyager jobs search get: "
        "rate limit: daily cap exceeded: 100 calls already made today (cap is 100)",
        # HTTP 999 and 403 fall through internal/voyager/client.go's
        # `status >= 400` branch (neither has a dedicated case like 401/429)
        # and come out as "voyager <path> returned HTTP <n>: <snippet>".
        # LinkedIn's anti-bot response is 999; a challenge/checkpoint is
        # 403 -- both are "we flagged this account" shapes, not ordinary
        # failures, so they must abort like a rate limit rather than
        # burning three more calls to confirm it.
        "jobs sweep: sweep search: voyager jobs search get: voyager "
        "/voyager/api/voyagerJobsDashJobCards returned HTTP 999: checkpoint required",
        "jobs sweep: sweep search: voyager jobs search get: voyager "
        "/voyager/api/voyagerJobsDashJobCards returned HTTP 403: forbidden",
    )

    def test_rate_limiting_aborts_instead_of_burning_every_archetype(self):
        for stderr in self.RATE_LIMIT_STDERRS:
            with self.subTest(stderr=stderr):
                runner = FakeRunner(default=FakeProc("", stderr, 1))
                with self.assertRaises(li_digest.AuthError):
                    li_digest.collect(self.cfg, run=runner, log=self.logs.append)
                self.assertEqual(len(runner.calls), 1)

    def test_two_consecutive_ordinary_failures_abort_without_a_matching_string(self):
        """No string list can be complete. Two ordinary (non-auth-pattern)
        failures in a row on the first two archetypes is itself a signal
        worth aborting on, even when neither stderr matches any known
        pattern -- this is the shape-independent circuit breaker."""
        data = json.loads(json.dumps(GOOD_CONFIG))
        data["archetypes"].append(
            {"name": "architect", "label": "Architect",
             "query": '"solution architect"', "match": "architect"}
        )
        cfg = li_digest.load_config(self.write_config(data, "three.json"))
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc("", "boom", 1),
            "engineering manager": FakeProc("", "also boom", 1),
            "solution architect": FakeProc(json.dumps(PLATFORM_JOBS)),
        })
        with self.assertRaises(li_digest.AuthError) as ctx:
            li_digest.collect(cfg, run=runner, log=self.logs.append)
        self.assertIn("two archetypes failed in a row", str(ctx.exception))
        self.assertEqual(len(runner.calls), 2)

    def test_valid_json_with_non_dict_elements_fails_gracefully(self):
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc(json.dumps(["not a dict", 42])),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        rows, failed = li_digest.collect(self.cfg, run=runner, log=self.logs.append)
        self.assertEqual(failed, ["platform"])
        self.assertEqual(len(rows), 2)


class TestRendering(ConfigTempDir):

    def setUp(self):
        super().setUp()
        self.cfg = li_digest.load_config(self.write_config(GOOD_CONFIG))
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc(json.dumps(PLATFORM_JOBS)),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        rows, _ = li_digest.collect(self.cfg, run=runner, log=lambda _m: None)
        self.rows = li_digest.enrich_rows(rows, self.cfg, date(2026, 7, 21))
        self.by_id = {r["urn"].rsplit(":", 1)[-1]: r for r in self.rows}

    def test_adds_multi_archetype_labels(self):
        self.assertEqual(self.by_id["222"]["archetypes"], "Platform, EM")

    def test_adds_link_and_bucket(self):
        self.assertEqual(self.by_id["111"]["link"],
                         "https://www.linkedin.com/jobs/view/111/")
        self.assertEqual(self.by_id["111"]["bucket"], "in")
        self.assertEqual(self.by_id["333"]["bucket"], "undated")

    def test_preserves_the_original_fields(self):
        self.assertEqual(self.by_id["111"]["company"]["name"], "Acme")

    def test_table_shows_titles_and_links(self):
        table = li_digest.render_table(self.rows)
        self.assertIn("Platform Engineer", table)
        self.assertIn("https://www.linkedin.com/jobs/view/111/", table)
        self.assertIn("Undated", table)
        self.assertIn("Platform, EM", table)

    def test_table_sorts_newest_first_within_a_bucket(self):
        # Create a fixture with rows in wrong order (oldest first, newest last)
        # to verify the sort actually runs and produces newest-first order.
        wrong_order = [
            li_digest.enrich_rows([job("444", "Terraform Lead", "OldCorp", "2026-08-02T00:00:00Z"),
                                   job("555", "Senior Platform", "NewCorp", "2026-08-05T00:00:00Z")],
                                  self.cfg, date(2026, 7, 21))
        ]
        rows = wrong_order[0]
        table = li_digest.render_table(rows)
        self.assertLess(table.index("2026-08-05"), table.index("2026-08-02"))

    def test_table_omits_empty_buckets(self):
        rows = [r for r in self.rows if r["bucket"] == "in"]
        table = li_digest.render_table(rows)
        self.assertNotIn("Undated", table)
        self.assertNotIn("Older than", table)

    def test_table_of_nothing_is_empty_not_a_crash(self):
        self.assertEqual(li_digest.render_table([]), "")

    def test_non_dict_company_and_non_string_title_do_not_crash(self):
        """A row with company as a bare string (instead of {"name": ...})
        used to raise AttributeError from company.get(...) in _cells; a
        non-string title used to raise TypeError from pattern.search(title)
        in labels_for via enrich_rows. Same defect class Task 6 already
        fixed in cmd_show -- render_table must return, not raise.

        posted_at as a bare int (e.g. 20260804) is the same defect class
        one field short: on Python 3.11+, date.fromisoformat accepts bare
        "YYYYMMDD", so bucket_of() happily buckets it as "in" via
        str(posted_at), and only _cells crashes, slicing the un-stringified
        int with [:10]. Degrades harmlessly to "undated" on 3.9/3.10, where
        fromisoformat rejects the dashless form -- but the guard must hold
        on every supported interpreter regardless of which branch fires.
        """
        bad_row = {
            "urn": "urn:li:fsd_jobPosting:999",
            "title": 42,
            "company": "Acme",
            "location": "Remote",
            "posted_at": 20260804,
            "origin": "Platform",
        }
        rows = li_digest.enrich_rows([bad_row], self.cfg, date(2026, 7, 21))
        table = li_digest.render_table(rows)
        self.assertIsInstance(table, str)
        self.assertIn("42", table)

    def test_json_output_round_trips(self):
        self.assertEqual(len(json.loads(json.dumps(self.rows))), 3)

    def test_undated_rows_show_dash_not_zero_date(self):
        table = li_digest.render_table(self.rows)
        self.assertNotIn("0001-01-01", table)

    def test_fresh_bucket_renders_first_with_its_own_heading(self):
        fresh_rows = li_digest.enrich_rows(
            [job("666", "Fresh Platform Role", "BrandNew", "2026-08-04T00:00:00Z")],
            self.cfg, date(2026, 7, 21), date(2026, 8, 4),
        )
        table = li_digest.render_table(fresh_rows + self.rows)
        self.assertIn("Posted since your last digest", table)
        self.assertLess(
            table.index("Posted since your last digest"), table.index("In window")
        )


import io  # noqa: E402  (grouped with the CLI tests for clarity)


class TestCli(ConfigTempDir):

    def setUp(self):
        super().setUp()
        self.path = self.write_config(GOOD_CONFIG)
        self.out = io.StringIO()
        self.err = io.StringIO()
        self.runner = FakeRunner(by_query={
            "platform engineer": FakeProc(json.dumps(PLATFORM_JOBS)),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })

    def run_cli(self, *argv, runner=None):
        return li_digest.main(
            list(argv), run=runner or self.runner,
            out=self.out, err=self.err, today=date(2026, 8, 4),
        )

    def seed(self):
        self.run_cli("--config", str(self.path), "--seed")
        self.out = io.StringIO()

    def test_refuses_before_seeding_and_names_the_fix(self):
        code = self.run_cli("--config", str(self.path))
        self.assertEqual(code, 2)
        self.assertIn("--seed", self.err.getvalue())
        self.assertEqual(self.out.getvalue(), "")

    def test_seed_writes_the_marker_and_prints_nothing_to_stdout(self):
        code = self.run_cli("--config", str(self.path), "--seed")
        self.assertEqual(code, 0)
        self.assertEqual(self.out.getvalue(), "")
        self.assertTrue(li_digest.seed_marker(self.path).exists())

    def test_partial_seed_names_the_failed_archetype_and_the_rerun_command(self):
        """A failed archetype's cache was never primed during --seed, so its
        entire backlog would surface as "new" on the next real run -- the
        opposite of what --seed promises. The old unconditional "seeded --
        the next run reports only genuinely new roles" message lied on a
        partial failure and gave no way to recover selectively."""
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc("", "boom", 1),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        code = self.run_cli("--config", str(self.path), "--seed", runner=runner)
        self.assertEqual(code, 1)
        # The marker is still written -- refusing would force a full
        # re-seed and burn more calls than just re-seeding the failed lane.
        self.assertTrue(li_digest.seed_marker(self.path).exists())
        stderr = self.err.getvalue()
        self.assertIn("platform", stderr)
        self.assertIn("--seed", stderr)
        self.assertIn("--only", stderr)
        self.assertIn("platform", stderr.split("--only", 1)[1])
        self.assertNotIn("only genuinely new roles", stderr)

    def test_marker_is_scoped_to_its_config(self):
        self.assertEqual(li_digest.seed_marker(self.path).parent, self.path.parent)

    def test_prints_a_table_after_seeding(self):
        self.seed()
        code = self.run_cli("--config", str(self.path))
        self.assertEqual(code, 0)
        self.assertIn("Platform Engineer", self.out.getvalue())

    def test_json_flag_emits_a_pipeable_array(self):
        self.seed()
        self.run_cli("--config", str(self.path), "--json")
        self.assertEqual(len(json.loads(self.out.getvalue())), 3)

    def test_nothing_new_prints_nothing_to_stdout_and_names_the_window_on_stderr(self):
        """The most common daily outcome is "nothing new". render_table([])
        returns "", so `print(render_table(rows))` put a bare "\n" on
        stdout with nothing on stderr to explain it -- indistinguishable
        from a bug. Table mode must print nothing to stdout and log to
        stderr naming the window instead."""
        empty_runner = FakeRunner(default=FakeProc("[]"))
        self.run_cli("--config", str(self.path), "--seed", runner=empty_runner)
        self.out, self.err = io.StringIO(), io.StringIO()
        code = self.run_cli("--config", str(self.path), runner=empty_runner)
        self.assertEqual(code, 0)
        self.assertEqual(self.out.getvalue(), "")
        self.assertIn("nothing new", self.err.getvalue())
        self.assertIn("14", self.err.getvalue())

    def test_nothing_new_still_emits_an_empty_json_array_in_json_mode(self):
        empty_runner = FakeRunner(default=FakeProc("[]"))
        self.run_cli("--config", str(self.path), "--seed", runner=empty_runner)
        self.out, self.err = io.StringIO(), io.StringIO()
        code = self.run_cli("--config", str(self.path), "--json", runner=empty_runner)
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(self.out.getvalue()), [])

    def test_only_restricts_to_named_archetypes(self):
        self.seed()
        self.run_cli("--config", str(self.path), "--only", "em", "--json")
        self.assertEqual(len(json.loads(self.out.getvalue())), 2)

    def test_only_rejects_an_unknown_name(self):
        self.seed()
        code = self.run_cli("--config", str(self.path), "--only", "nope")
        self.assertEqual(code, 2)
        self.assertIn("unknown archetype", self.err.getvalue())

    def test_window_narrows_the_in_bucket(self):
        """With today=2026-08-04, the fixture jobs are dated 2026-08-04 and
        2026-08-03. The DEFAULT 14-day window already puts both in-bucket, so
        a --window value that ALSO yields 2 (e.g. 3650) proves nothing: the
        assertion would still pass even if --window were parsed and then
        silently ignored. --window 0 is the one value only a working flag
        can produce: it narrows the cutoff to today, dropping the
        2026-08-03 job to "old" and leaving exactly 1 in-bucket row.
        """
        self.seed()
        code = self.run_cli("--config", str(self.path), "--window", "0", "--json")
        self.assertEqual(code, 0)
        rows = json.loads(self.out.getvalue())
        self.assertEqual(len([r for r in rows if r["bucket"] == "in"]), 1)

    def test_exits_1_when_an_archetype_failed_but_still_prints(self):
        runner = FakeRunner(by_query={
            "platform engineer": FakeProc("", "boom", 1),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        self.run_cli("--config", str(self.path), "--seed", runner=runner)
        self.out = io.StringIO()
        code = self.run_cli("--config", str(self.path), "--json", runner=runner)
        self.assertEqual(code, 1)
        self.assertEqual(len(json.loads(self.out.getvalue())), 2)

    def test_dead_session_exits_2_with_the_login_hint(self):
        runner = FakeRunner(default=FakeProc("", "not logged in", 1))
        code = self.run_cli("--config", str(self.path), "--seed", runner=runner)
        self.assertEqual(code, 2)
        self.assertIn("auth login", self.err.getvalue())

    def test_missing_config_exits_2(self):
        """The wording changed when the missing-config path started
        explaining how to create the file; the contract did not. Asserted
        on the invariants -- exit 2, the path named, stdout untouched --
        rather than on a phrase, so improving the message again does not
        break this."""
        code = self.run_cli("--config", str(self.tmp / "nope.json"))
        self.assertEqual(code, 2)
        self.assertIn("nope.json", self.err.getvalue())
        self.assertEqual(self.out.getvalue(), "")

    def test_an_unreadable_config_that_EXISTS_still_says_not_readable(self):
        """A file present but unreadable is a different problem from an
        absent one, and must not be answered with setup instructions."""
        path = self.tmp / "locked.json"
        path.write_text("{}", encoding="utf-8")
        path.chmod(0o000)
        self.addCleanup(path.chmod, 0o600)
        code = self.run_cli("--config", str(path))
        self.assertEqual(code, 2)
        self.assertIn("not readable", self.err.getvalue())
        self.assertNotIn("no archetypes file yet", self.err.getvalue())

    def test_malformed_argument_exits_2_without_raising(self):
        code = self.run_cli("--config", str(self.path), "--window", "notanumber")
        self.assertEqual(code, 2)
        self.assertNotEqual(self.err.getvalue(), "")

    def test_unknown_flag_exits_2_without_raising(self):
        code = self.run_cli("--config", str(self.path), "--nope")
        self.assertEqual(code, 2)
        self.assertNotEqual(self.err.getvalue(), "")

    def test_help_returns_0_and_writes_to_injected_out(self):
        code = self.run_cli("--help")
        self.assertEqual(code, 0)
        self.assertIn("li-digest", self.out.getvalue())

    def test_only_rejects_all_blank_names(self):
        code = self.run_cli("--config", str(self.path), "--only", ",,")
        self.assertEqual(code, 2)
        self.assertIn("--only", self.err.getvalue())

    def test_seed_does_not_write_the_last_run_marker(self):
        """Seeding suppresses output, so advancing the last-run stamp there
        would make the first REAL run show nothing as fresh."""
        self.seed()
        self.assertFalse(li_digest.last_run_marker(self.path).exists())

    def test_first_real_run_has_no_fresh_bucket_and_writes_the_marker(self):
        self.seed()
        self.assertFalse(li_digest.last_run_marker(self.path).exists())
        code = self.run_cli("--config", str(self.path), "--json")
        self.assertEqual(code, 0)
        rows = json.loads(self.out.getvalue())
        self.assertEqual([r["bucket"] for r in rows].count("fresh"), 0)
        self.assertTrue(li_digest.last_run_marker(self.path).exists())

    def test_second_run_buckets_a_newer_posting_as_fresh(self):
        """Fixture jobs are dated 2026-08-04 (urn 111) and 2026-08-03 (urn
        222). run_cli always injects today=2026-08-04, so the first run's
        last-run stamp lands on 2026-08-04 too -- job 111 (posted the same
        day) must come back fresh on the second run; job 222 (posted the
        day before) must fall back to the ordinary in-window bucket."""
        self.seed()
        self.run_cli("--config", str(self.path), "--json")  # first real run
        self.out = io.StringIO()
        self.run_cli("--config", str(self.path), "--json")  # second real run
        rows = {r["urn"].rsplit(":", 1)[-1]: r for r in json.loads(self.out.getvalue())}
        self.assertEqual(rows["111"]["bucket"], "fresh")
        self.assertEqual(rows["222"]["bucket"], "in")

    def test_malformed_last_run_marker_degrades_to_no_fresh_bucket(self):
        self.seed()
        self.run_cli("--config", str(self.path), "--json")  # writes a valid marker
        li_digest.last_run_marker(self.path).write_text("garbage", encoding="utf-8")
        self.out = io.StringIO()
        self.run_cli("--config", str(self.path), "--json")
        rows = {r["urn"].rsplit(":", 1)[-1]: r for r in json.loads(self.out.getvalue())}
        self.assertEqual(rows["111"]["bucket"], "in")

    def test_future_last_run_marker_degrades_to_no_fresh_bucket(self):
        self.seed()
        self.run_cli("--config", str(self.path), "--json")  # writes a valid marker
        li_digest.last_run_marker(self.path).write_text(
            "9999-01-01T00:00:00Z", encoding="utf-8"
        )
        self.out = io.StringIO()
        self.run_cli("--config", str(self.path), "--json")
        rows = {r["urn"].rsplit(":", 1)[-1]: r for r in json.loads(self.out.getvalue())}
        self.assertEqual(rows["111"]["bucket"], "in")

    def test_last_run_marker_is_written_after_output_not_before(self):
        """A crash mid-render must not leave the stamp advanced -- simulate
        that crash by making the output stream raise on write()."""
        self.seed()

        class BoomOut:
            def write(self, _s):
                raise RuntimeError("boom mid-render")

        with self.assertRaises(RuntimeError):
            li_digest.main(
                ["--config", str(self.path)],
                run=self.runner, out=BoomOut(), err=self.err,
                today=date(2026, 8, 4),
            )
        self.assertFalse(li_digest.last_run_marker(self.path).exists())

    def test_marker_write_failure_does_not_crash_a_successful_run(self):
        """An unwritable marker path (read-only/full config dir, in the
        real world) must not turn an otherwise successful run into an
        uncaught traceback AFTER the table already printed -- that breaks
        the 0/1/2 exit-code contract on the ordinary daily path. A missing
        marker already degrades gracefully (no fresh bucket next time); an
        unwritable one must too, just with a note on stderr."""
        self.seed()
        li_digest.last_run_marker(self.path).mkdir()  # write_text -> IsADirectoryError
        code = self.run_cli("--config", str(self.path), "--json")
        self.assertEqual(code, 0)
        self.assertIn("could not update the last-run marker", self.err.getvalue())

    def test_marker_is_unchanged_after_a_run_that_exits_1(self):
        """A run where an archetype failed must not advance the GLOBAL
        last-run stamp: that lane's diff for this run is unreliable input,
        and a partially-failed run has no business promising "I looked at
        everything". Proven by seeding a SENTINEL into the marker and
        checking it survives untouched -- writing the SAME valid stamp
        twice would look identical and prove nothing."""
        self.seed()
        self.run_cli("--config", str(self.path), "--json")  # writes a valid marker
        li_digest.last_run_marker(self.path).write_text("SENTINEL", encoding="utf-8")
        failing_runner = FakeRunner(by_query={
            "platform engineer": FakeProc("", "boom", 1),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        self.out = io.StringIO()
        code = self.run_cli("--config", str(self.path), "--json", runner=failing_runner)
        self.assertEqual(code, 1)
        self.assertEqual(
            li_digest.last_run_marker(self.path).read_text(encoding="utf-8"), "SENTINEL"
        )

    def test_marker_is_unchanged_after_an_only_run(self):
        """A narrowed --only run never swept the untouched lane(s) at all,
        so it must not advance a GLOBAL last-run stamp on their behalf."""
        self.seed()
        self.run_cli("--config", str(self.path), "--json")  # writes a valid marker
        li_digest.last_run_marker(self.path).write_text("SENTINEL", encoding="utf-8")
        self.out = io.StringIO()
        code = self.run_cli("--config", str(self.path), "--only", "em", "--json")
        self.assertEqual(code, 0)
        self.assertEqual(
            li_digest.last_run_marker(self.path).read_text(encoding="utf-8"), "SENTINEL"
        )

    def test_main_threads_its_today_through_to_read_last_run(self):
        """Asserts the WIRING, not just an outcome: read_last_run's `today`
        kwarg is useless if main() never passes it (the other half of the
        UTC fix -- read_last_run alone can't prove main() calls it
        correctly). Patches read_last_run itself and checks the exact
        kwarg arrives. The runner returns nothing swept, so the patched
        function's default MagicMock return value never has to survive a
        real bucket_of comparison."""
        empty_runner = FakeRunner(default=FakeProc("[]"))
        self.run_cli("--config", str(self.path), "--seed", runner=empty_runner)
        self.out = io.StringIO()
        with mock.patch("li_digest.read_last_run") as mock_read_last_run:
            mock_read_last_run.return_value = None
            code = self.run_cli("--config", str(self.path), "--json", runner=empty_runner)
        self.assertEqual(code, 0)
        mock_read_last_run.assert_called_once()
        _, kwargs = mock_read_last_run.call_args
        self.assertEqual(kwargs.get("today"), date(2026, 8, 4))


class TestRemoteFilter(ConfigTempDir):
    """li-assist has no server-side workplace filter (LinkedIn moved it to
    SDUI), but every row's `location` already carries a (Remote) / (Hybrid)
    / (On-site) marker -- so --remote filters locally, for free."""

    def setUp(self):
        super().setUp()
        self.path = self.write_config(GOOD_CONFIG)
        self.out = io.StringIO()
        self.err = io.StringIO()

    def run_cli(self, *argv, runner):
        return li_digest.main(
            list(argv), run=runner, out=self.out, err=self.err, today=date(2026, 8, 4),
        )

    def seed(self, runner):
        self.run_cli("--config", str(self.path), "--seed", runner=runner)
        self.out = io.StringIO()

    def runner_with(self, jobs):
        return FakeRunner(by_query={
            "platform engineer": FakeProc(json.dumps(jobs)),
            "engineering manager": FakeProc(json.dumps([])),
        })

    def test_remote_rows_are_kept_and_others_dropped(self):
        jobs = [
            job("1", "Remote Role", location="Germany (Remote)"),
            job("2", "Hybrid Role", location="Germany (Hybrid)"),
            job("3", "Onsite Role", location="Germany (On-site)"),
            job("4", "Unmarked Role", location="Germany"),
        ]
        runner = self.runner_with(jobs)
        self.seed(runner)
        code = self.run_cli("--config", str(self.path), "--remote", "--json", runner=runner)
        self.assertEqual(code, 0)
        rows = json.loads(self.out.getvalue())
        self.assertEqual([r["title"] for r in rows], ["Remote Role"])

    def test_undated_remote_row_survives_the_filter_and_stays_undated(self):
        """--remote is the one place this feature is licensed to drop
        rows, and undated postings must never be silently dropped -- prove
        the two rules coexist: an undated row WITH the marker survives and
        keeps its "undated" bucket (not promoted to "in" or anything else
        just because it passed the filter)."""
        jobs = [job("1", "Undated Remote Role", posted=li_digest.ZERO_DATE,
                     location="Germany (Remote)")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        self.run_cli("--config", str(self.path), "--remote", "--json", runner=runner)
        rows = json.loads(self.out.getvalue())
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["bucket"], "undated")

    def test_undated_unmarked_row_is_dropped_by_the_filter(self):
        """The other half of the same rule: an undated row is not exempt
        from --remote just because it's undated -- with no (Remote) marker
        it is excluded like any other unmarked row."""
        jobs = [job("1", "Undated Unmarked Role", posted=li_digest.ZERO_DATE,
                     location="Germany")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        self.run_cli("--config", str(self.path), "--remote", "--json", runner=runner)
        self.assertEqual(json.loads(self.out.getvalue()), [])

    def test_matching_is_case_insensitive(self):
        jobs = [job("1", "Remote Role", location="Germany (REMOTE)")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        self.run_cli("--config", str(self.path), "--remote", "--json", runner=runner)
        rows = json.loads(self.out.getvalue())
        self.assertEqual(len(rows), 1)

    def test_remote_filter_applies_to_table_mode_too(self):
        jobs = [job("1", "Remote Role", location="Germany (Remote)"),
                job("2", "Hybrid Role", location="Germany (Hybrid)")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        self.run_cli("--config", str(self.path), "--remote", runner=runner)
        table = self.out.getvalue()
        self.assertIn("Remote Role", table)
        self.assertNotIn("Hybrid Role", table)

    def test_unmarked_locations_are_excluded_not_assumed_remote(self):
        jobs = [job("1", "Unmarked Role", location="Germany")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        self.run_cli("--config", str(self.path), "--remote", "--json", runner=runner)
        self.assertEqual(json.loads(self.out.getvalue()), [])

    def test_empty_after_filter_names_the_filter_not_a_plain_nothing_new(self):
        jobs = [job("1", "Hybrid Role", location="Germany (Hybrid)")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        code = self.run_cli("--config", str(self.path), "--remote", runner=runner)
        self.assertEqual(code, 0)
        self.assertEqual(self.out.getvalue(), "")
        self.assertIn("--remote", self.err.getvalue())
        self.assertNotIn("nothing new in the last", self.err.getvalue())

    def test_empty_after_filter_in_json_mode_is_still_an_empty_array(self):
        jobs = [job("1", "Hybrid Role", location="Germany (Hybrid)")]
        runner = self.runner_with(jobs)
        self.seed(runner)
        code = self.run_cli("--config", str(self.path), "--remote", "--json", runner=runner)
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(self.out.getvalue()), [])

    def test_genuinely_nothing_new_keeps_the_ordinary_message_even_with_remote(self):
        """--remote must not claim credit for an empty result it didn't
        cause -- when there was nothing to filter, say so plainly."""
        runner = FakeRunner(default=FakeProc("[]"))
        self.seed(runner)
        code = self.run_cli("--config", str(self.path), "--remote", runner=runner)
        self.assertEqual(code, 0)
        self.assertIn("nothing new in the last", self.err.getvalue())


DETAIL = {
    "urn": "urn:li:fsd_jobPosting:4445664999",
    "title": "Forward-Deployed Engineer - Agentic Systems",
    "location": "Greater Munich Metropolitan Area",
    "company": {"urn": "urn:li:fsd_company:219816", "name": "ESPRiT Engineering"},
    "posted_at": li_digest.ZERO_DATE,
    "posting": {"description": "Build LLM multi-agent systems.",
                "apply_url": "https://www.linkedin.com/job-apply/4445664999",
                "applicant_count": 0},
}


class TestShow(unittest.TestCase):

    def setUp(self):
        self.out = io.StringIO()
        self.err = io.StringIO()
        self.runner = FakeRunner(default=FakeProc(json.dumps(DETAIL)))

    def test_prints_description_and_both_urls(self):
        code = li_digest.main(["show", "urn:li:fsd_jobPosting:4445664999"],
                              run=self.runner, out=self.out, err=self.err)
        self.assertEqual(code, 0)
        text = self.out.getvalue()
        self.assertIn("Build LLM multi-agent systems.", text)
        self.assertIn("https://www.linkedin.com/job-apply/4445664999", text)
        self.assertIn("https://www.linkedin.com/jobs/view/4445664999/", text)

    def test_accepts_a_bare_numeric_id(self):
        li_digest.main(["show", "4445664999"], run=self.runner, out=self.out, err=self.err)
        self.assertIn("ESPRiT Engineering", self.out.getvalue())
        self.assertIn("urn:li:fsd_jobPosting:4445664999", self.runner.calls[0])

    def test_never_passes_enrich(self):
        li_digest.main(["show", "4445664999"], run=self.runner, out=self.out, err=self.err)
        self.assertNotIn("--enrich", self.runner.calls[0])

    def test_requires_an_argument(self):
        code = li_digest.main(["show"], run=self.runner, out=self.out, err=self.err)
        self.assertEqual(code, 2)
        self.assertIn("needs a job urn or id", self.err.getvalue())

    def test_missing_description_does_not_crash(self):
        payload = json.loads(json.dumps(DETAIL))
        del payload["posting"]["description"]
        runner = FakeRunner(default=FakeProc(json.dumps(payload)))
        code = li_digest.main(["show", "4445664999"], run=runner, out=self.out, err=self.err)
        self.assertEqual(code, 0)
        self.assertIn("no description", self.out.getvalue())

    # Real strings, same provenance as TestCollect.DEAD_SESSION_STDERRS: the
    # local not-logged-in check is identical across search/get/sweep in
    # cmd/li-assist/jobs.go; the voyager-401 chain for `jobs get` wraps
    # domain.ErrAuth through internal/voyager/jobs.go's "voyager job detail
    # get: ..." and cmd/li-assist/jobs.go's "jobs get: ...".
    DEAD_SESSION_STDERRS = (
        "not logged in -- run 'li-assist auth login' to open a browser "
        "window and sign in; completing 2FA is normal and expected; tick "
        '"Keep me logged in" so the session persists across commands',
        "jobs get: voyager job detail get: authentication failed: "
        "re-run li-assist auth login",
    )

    def test_dead_session_exits_2(self):
        for stderr in self.DEAD_SESSION_STDERRS:
            with self.subTest(stderr=stderr):
                runner = FakeRunner(default=FakeProc("", stderr, 1))
                out, err = io.StringIO(), io.StringIO()
                code = li_digest.main(["show", "4445664999"], run=runner, out=out, err=err)
                self.assertEqual(code, 2)
                # Must be the crafted AuthError message, not merely a
                # ConfigError echoing stderr verbatim (which would also
                # contain "auth login" by accident, since the real wording
                # contains "re-run li-assist auth login") -- that accidental
                # match is exactly the defect this test replaces.
                self.assertIn("session is not usable", err.getvalue())
                self.assertIn("auth login", err.getvalue())

    def test_builds_a_jobs_get_command(self):
        """The other tests use a catch-all fake, so assert the argv shape once."""
        li_digest.main(["show", "4445664999"], run=self.runner, out=self.out, err=self.err)
        self.assertEqual(len(self.runner.calls), 1)
        cmd = self.runner.calls[0]
        self.assertEqual(cmd[:3], ["li-assist", "jobs", "get"])
        self.assertIn("--format", cmd)
        self.assertIn("json", cmd)

    def test_rejects_a_foreign_urn_type_before_any_call(self):
        code = li_digest.main(["show", "urn:li:fsd_company:123"],
                              run=self.runner, out=self.out, err=self.err)
        self.assertEqual(code, 2)
        self.assertIn("urn:li:fsd_company:123", self.err.getvalue())
        self.assertEqual(self.runner.calls, [])

    def test_rejects_a_non_numeric_bare_id_before_any_call(self):
        """The else-branch used to accept ANY non-"urn:li:" string as a
        bare id, so `li-digest show --json` built the argv
        ["li-assist", "jobs", "get", "urn:li:fsd_jobPosting:--json", ...]
        and spawned it for real."""
        code = li_digest.main(["show", "--json"], run=self.runner, out=self.out, err=self.err)
        self.assertEqual(code, 2)
        self.assertIn("--json", self.err.getvalue())
        self.assertEqual(self.runner.calls, [])

    def test_rejects_a_full_urn_with_an_empty_tail_before_any_call(self):
        code = li_digest.main(["show", "urn:li:fsd_jobPosting:"],
                              run=self.runner, out=self.out, err=self.err)
        self.assertEqual(code, 2)
        self.assertEqual(self.runner.calls, [])

    def test_rejects_unicode_digit_bare_ids_before_any_call(self):
        """re.fullmatch(r"\\d+") matches any Unicode decimal digit, not just
        ASCII 0-9 -- Arabic-Indic numerals (e.g. "١٢٣") would still pass the
        old check and spend a real call on a urn li-assist cannot parse."""
        code = li_digest.main(["show", "١٢٣"],
                              run=self.runner, out=self.out, err=self.err)
        self.assertEqual(code, 2)
        self.assertEqual(self.runner.calls, [])

    def test_non_auth_failure_exits_2_with_the_stderr_message(self):
        runner = FakeRunner(default=FakeProc("", "rate limit exceeded", 1))
        code = li_digest.main(["show", "4445664999"], run=runner, out=self.out, err=self.err)
        self.assertEqual(code, 2)
        self.assertIn("rate limit exceeded", self.err.getvalue())

    def test_non_object_payload_does_not_crash(self):
        """Valid JSON of the wrong shape must not raise a bare AttributeError."""
        for payload in ("[]", '"a string"', "42"):
            with self.subTest(payload=payload):
                runner = FakeRunner(default=FakeProc(payload))
                err = io.StringIO()
                code = li_digest.main(["show", "4445664999"],
                                      run=runner, out=io.StringIO(), err=err)
                self.assertEqual(code, 2)
                self.assertIn("expected an object", err.getvalue())

    def test_non_object_posting_and_company_do_not_crash(self):
        payload = json.loads(json.dumps(DETAIL))
        payload["posting"] = "not an object"
        payload["company"] = "also not an object"
        runner = FakeRunner(default=FakeProc(json.dumps(payload)))
        code = li_digest.main(["show", "4445664999"], run=runner, out=self.out, err=self.err)
        self.assertEqual(code, 0)
        self.assertIn("no description", self.out.getvalue())


# The SKILL.md doc-drift guard (checks documented `li-digest ...` / `li-report
# ...` invocation flags against each tool's real build_parser()) has moved to
# test_li_report.py's TestDocsMatchImplementation, generalized to cover both
# tools in one place. It moved rather than staying split across two files
# because checking li-report's flags means importing li_report, and this
# file has no other reason to know that module exists.


import os  # noqa: E402  (grouped with the pipe tests below)
import subprocess as _subprocess  # noqa: E402


def close_pipe_early(argv, env):
    """Run argv, read one line of stdout, close the pipe. Returns
    (returncode, stderr).

    This is what `| head -1` does to a producer, minus the shell: a shell
    pipeline's exit status is the LAST command's, so `sh -c "prog | head"`
    reports head's 0 no matter how prog dies, and any assertion on it is
    inert. Driving the pipe directly keeps the child's own status.
    """
    proc = _subprocess.Popen(argv, stdout=_subprocess.PIPE,
                             stderr=_subprocess.PIPE, env=env)
    try:
        proc.stdout.readline()
        proc.stdout.close()
        stderr = proc.stderr.read().decode("utf-8", "replace")
    finally:
        proc.stderr.close()
        proc.wait()
    return proc.returncode, stderr


class _DeadPipe(io.StringIO):
    """A stdout whose first write fails the way `| head` makes it fail."""

    def write(self, _data):
        raise BrokenPipeError(32, "Broken pipe")


class TestBrokenPipe(ConfigTempDir):
    """`li-digest | head` is the obvious way to peek at a long table, and it
    used to end in a BrokenPipeError traceback and a non-zero exit on an
    otherwise successful run. head closing its stdin early is normal
    pipeline behaviour, not a failure.
    """

    def setUp(self):
        super().setUp()
        self.path = self.write_config(GOOD_CONFIG)
        self.err = io.StringIO()
        self.runner = FakeRunner(by_query={
            "platform engineer": FakeProc(json.dumps(PLATFORM_JOBS)),
            "engineering manager": FakeProc(json.dumps(EM_JOBS)),
        })
        li_digest.main(
            ["--config", str(self.path), "--seed"], run=self.runner,
            out=io.StringIO(), err=self.err, today=date(2026, 8, 4),
        )

    def run_with_dead_pipe(self, *argv):
        return li_digest.main(
            ["--config", str(self.path), *argv], run=self.runner,
            out=_DeadPipe(), err=self.err, today=date(2026, 8, 4),
        )

    def test_table_output_exits_0_on_a_closed_pipe(self):
        self.assertEqual(self.run_with_dead_pipe(), 0)

    def test_json_output_exits_0_on_a_closed_pipe(self):
        """--json writes through a different print() call than the table, so
        it needs its own case -- a handler wrapped around only the table
        branch would leave `li-digest --json | head` still crashing."""
        self.assertEqual(self.run_with_dead_pipe("--json"), 0)

    def test_nothing_is_reported_as_an_error_on_stderr(self):
        before = self.err.getvalue()
        self.run_with_dead_pipe()
        self.assertNotIn("BrokenPipe", self.err.getvalue()[len(before):])

    def test_the_last_run_stamp_is_not_advanced(self):
        """The deliberate half of the fix. Truncating with `head` means you
        did NOT read every new posting, so advancing the stamp would demote
        the rows you never saw from "fresh" to merely "in window" on the
        next run. Leaving it unwritten costs no extra calls."""
        stamp = li_digest.last_run_marker(self.path)
        self.assertFalse(stamp.exists())
        self.run_with_dead_pipe()
        self.assertFalse(
            stamp.exists(),
            "a run truncated by `| head` must not advance the last-run stamp",
        )

    def test_a_clean_run_still_advances_it(self):
        """Pins the test above to the pipe, not to a stamp that never
        writes: same fixture, live stdout, stamp appears."""
        stamp = li_digest.last_run_marker(self.path)
        li_digest.main(
            ["--config", str(self.path)], run=self.runner,
            out=io.StringIO(), err=self.err, today=date(2026, 8, 4),
        )
        self.assertTrue(stamp.exists())


class TestBrokenPipeInARealPipeline(ConfigTempDir):
    """The in-process tests above cannot see the failure this actually
    fixes. Python flushes stdout at interpreter shutdown, so even with the
    exception handled, a real `li-digest | head` printed

        Exception ignored on flushing sys.stdout: BrokenPipeError

    to stderr and exited non-zero AFTER main returned 0. Only a real
    process with a real pipe reproduces it, so this test builds one: a stub
    `li-assist` on PATH, then an actual shell pipeline into `head`.
    """

    def setUp(self):
        super().setUp()
        self.path = self.write_config(GOOD_CONFIG)
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        # Big enough to overflow the OS pipe buffer, and dated off the real
        # clock. Both matter:
        #
        # A 2-row table is ~200 bytes. The reader can close its end and the
        # writer still completes, because the whole table fits in the 64 KiB
        # pipe buffer -- no BrokenPipeError is ever raised, and a test built
        # on that fixture passes with the handler deleted. 4000 rows is
        # ~400 KB, which cannot fit, so the write genuinely fails.
        #
        # The dates come from datetime.now rather than a 2026 literal
        # because these rows must fall inside --window's 14 days. A hardcoded
        # date silently ages out of the window, the table renders empty, and
        # the test goes quiet again -- the same inert failure by a slower
        # route.
        today = datetime.now(timezone.utc).strftime("%Y-%m-%dT00:00:00Z")
        many = [job(str(i), f"Platform Engineer {i}", posted=today) for i in range(4000)]
        stub = self.bin / "li-assist"
        stub.write_text(
            "#!/bin/sh\n"
            f"cat <<'JSON'\n{json.dumps(many)}\nJSON\n",
            encoding="utf-8",
        )
        stub.chmod(0o755)
        self.env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}",
                    "PYTHONDONTWRITEBYTECODE": "1"}
        self.script = str(Path(li_digest.__file__).resolve())
        seed = _subprocess.run(
            [sys.executable, self.script, "--config", str(self.path), "--seed"],
            capture_output=True, text=True, env=self.env,
        )
        self.assertEqual(seed.returncode, 0, seed.stderr)

    def run_into_a_closed_pipe(self, *argv):
        """`| head` without a shell.

        A `sh -c "... | head -1"` would report HEAD's exit status, not
        Python's, so an assertion on it could never fail -- reading one line
        and closing the pipe reproduces the same condition while keeping the
        child's real returncode.
        """
        return close_pipe_early(
            [sys.executable, self.script, "--config", str(self.path), *argv], self.env)

    def test_piping_into_head_is_silent_and_exits_0(self):
        code, stderr = self.run_into_a_closed_pipe()
        self.assertNotIn("BrokenPipeError", stderr)
        self.assertNotIn("Exception ignored", stderr)
        self.assertEqual(code, 0, stderr)

    def test_json_piped_into_head_is_silent_and_exits_0(self):
        code, stderr = self.run_into_a_closed_pipe("--json")
        self.assertNotIn("BrokenPipeError", stderr)
        self.assertNotIn("Exception ignored", stderr)
        self.assertEqual(code, 0, stderr)


class TestSilenceBrokenPipe(unittest.TestCase):
    """_silence_broken_pipe's CALL SITE in main survives deletion, because
    both output branches emit the document in one print() and nothing is
    left buffered when that write fails. An untested defensive call is how
    dead code accumulates, so the helper's own contract is asserted here
    directly: after it runs, the real stdout fd points at /dev/null, so
    anything written afterwards -- including CPython's flush at interpreter
    shutdown -- goes nowhere instead of at a dead pipe.

    Asserting the CONTRACT rather than the symptom, deliberately. The
    symptom (`Exception ignored in: <_io.TextIOWrapper name='<stdout>'>`
    plus exit 120 from the shutdown flush) depends on how much data is
    still buffered when the pipe dies, and that varies by platform and
    CPython version: a 300k-line print-per-line loop reproduces it on macOS
    3.9/3.10 and on Linux 3.14, but not on Linux 3.9/3.10/3.13. A test
    pinned to the symptom is red on half the support matrix while the
    helper works perfectly on all of it. The redirect itself is
    deterministic everywhere.
    """

    SCRIPT = (
        "import sys\n"
        "sys.path.insert(0, {scripts!r})\n"
        "import li_digest\n"
        "sys.stdout.write('before\\n')\n"
        "sys.stdout.flush()\n"
        "{call}\n"
        "sys.stdout.write('after\\n')\n"
        "sys.stdout.flush()\n"
    )

    def run_capturing_stdout(self, call):
        scripts = str(Path(li_digest.__file__).resolve().parent)
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.py"
            probe.write_text(self.SCRIPT.format(scripts=scripts, call=call), encoding="utf-8")
            landed = Path(tmp) / "stdout.txt"
            with landed.open("w") as sink:
                proc = _subprocess.run(
                    [sys.executable, str(probe)], stdout=sink, stderr=_subprocess.PIPE,
                    env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
                )
            self.assertEqual(proc.returncode, 0, proc.stderr.decode("utf-8", "replace"))
            return landed.read_text(encoding="utf-8")

    def test_writes_after_the_helper_go_to_devnull(self):
        landed = self.run_capturing_stdout("li_digest._silence_broken_pipe()")
        self.assertIn("before", landed)
        self.assertNotIn("after", landed)

    def test_control_without_the_helper_both_writes_land(self):
        """Pins the test above to the helper rather than to some other
        reason 'after' might go missing."""
        landed = self.run_capturing_stdout("pass")
        self.assertIn("before", landed)
        self.assertIn("after", landed)


class TestFirstRunExperience(ConfigTempDir):
    """A new user's first `li-digest` hits a missing archetypes.json, and
    the answer was "config not readable: <path>" -- accurate, and useless.

    It matters more here than in a tool with no credentials or quota. Each
    archetype costs one API call per run against a 100/day cap on a real
    LinkedIn account, so a config someone guessed at is not merely noisy,
    it is expensive. And every lane needs TWO fields kept in agreement,
    which is this project's named top risk.
    """

    def run_cli(self, *argv):
        out, err = io.StringIO(), io.StringIO()
        code = li_digest.main(list(argv), run=FakeRunner(by_query={}),
                              out=out, err=err, today=date(2026, 8, 5))
        return code, out.getvalue(), err.getvalue()

    def test_a_missing_config_explains_what_the_file_is_for(self):
        code, out, err = self.run_cli("--config", str(self.tmp / "absent.json"))
        self.assertEqual(code, 2)
        self.assertEqual(out, "", "stdout stays data-only")
        lowered = err.lower()
        self.assertIn("absent.json", err, "must name the path it looked at")
        self.assertIn("no archetypes file yet", lowered)
        self.assertIn("query", lowered)
        self.assertIn("match", lowered)

    def test_it_warns_that_each_archetype_costs_a_call(self):
        """The cap is the thing a guessed config actually burns."""
        _, _, err = self.run_cli("--config", str(self.tmp / "absent.json"))
        self.assertIn("100", err)

    def test_a_malformed_config_is_not_reported_as_missing(self):
        path = self.tmp / "broken.json"
        path.write_text("{not json", encoding="utf-8")
        code, _, err = self.run_cli("--config", str(path))
        self.assertEqual(code, 2)
        self.assertNotIn("no archetypes file yet", err.lower())
        self.assertIn("not valid json", err.lower())

    def test_a_valid_config_is_unaffected(self):
        path = self.write_config(GOOD_CONFIG)
        code, _, err = self.run_cli("--config", str(path))
        self.assertNotIn("no archetypes file yet", err.lower())
        self.assertEqual(code, 2, "still refuses to run unseeded")
        self.assertIn("--seed", err)


if __name__ == "__main__":
    unittest.main()
