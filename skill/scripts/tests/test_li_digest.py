"""Stdlib-only tests for li_digest. No network, no LinkedIn session."""

import json
import re
import sys
import tempfile
import unittest
from datetime import date
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import li_digest  # noqa: E402


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


def job(urn, title, company="Acme", posted="2026-08-04T00:00:00Z"):
    return {"urn": f"urn:li:fsd_jobPosting:{urn}", "title": title,
            "location": "Germany (Remote)", "company": {"urn": "", "name": company},
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
        code = self.run_cli("--config", str(self.tmp / "nope.json"))
        self.assertEqual(code, 2)
        self.assertIn("not readable", self.err.getvalue())

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


class TestDocsMatchImplementation(unittest.TestCase):
    """SKILL.md must not document flags the parser does not have."""

    def test_documented_flags_all_exist(self):
        """Only flags shown in a li-digest *invocation* are checked — prose
        naming another tool's flags (e.g. li-assist's --enrich) is exempt by
        construction, so authors don't have to avoid real flag names in
        prose to keep this guard green.
        """
        skill_md = Path(__file__).resolve().parents[2] / "SKILL.md"
        text = skill_md.read_text(encoding="utf-8")
        section = text.split("## Archetype digest", 1)[1].split("\n## ", 1)[0]
        fences = re.findall(r"```(?:[a-zA-Z]*)\n(.*?)\n```", section, re.DOTALL)
        li_digest_blocks = [
            block for block in fences
            if next(
                (line.strip() for line in block.splitlines() if line.strip()), ""
            ).startswith("li-digest")
        ]
        documented = set()
        for block in li_digest_blocks:
            documented.update(re.findall(r"(?<![\w-])--[a-z][a-z-]+", block))
        parser_flags = set()
        for action in li_digest.build_parser()._actions:
            parser_flags.update(opt for opt in action.option_strings if opt.startswith("--"))
        self.assertTrue(
            documented,
            "found no flags in any li-digest command block — check the "
            "heading split and the li-digest fence filter",
        )
        self.assertEqual(documented - parser_flags, set())


if __name__ == "__main__":
    unittest.main()
