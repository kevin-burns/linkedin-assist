#!/usr/bin/env python3
"""li-digest — sweep every CV archetype in one run and print what is new.

Reads ~/.config/li-assist/archetypes.json, runs ONE `li-assist jobs sweep` per
archetype (sequential — never parallel), merges the results, labels each job
against every archetype's regex, and buckets by posting date.

stdout is data (table or --json). stderr is human.
Exit: 0 clean, 1 one or more archetypes failed, 2 config or usage error.

Supported platforms: macOS and Linux, matching what li-assist ships.
Windows users need WSL.

Standard library only — no jq, no third-party packages.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import dataclass, replace
from datetime import date, datetime, timedelta, timezone
from pathlib import Path

CONFIG_DIR = Path.home() / ".config" / "li-assist"
CONFIG_DEFAULT = CONFIG_DIR / "archetypes.json"

ZERO_DATE = "0001-01-01T00:00:00Z"
VIEW_URL = "https://www.linkedin.com/jobs/view/{job_id}/"
REQUIRED_FIELDS = ("name", "label", "query", "match")

# A dead session fails every archetype identically, so it is worth detecting
# rather than burning three more calls to learn the same thing.
#
# Grounded in the real Go source, not imagination:
#   - "not logged in" -- the LOCAL cookie-presence check, identical wording
#     across search/get/sweep (cmd/li-assist/jobs.go).
#   - "authentication failed" -- domain.ErrAuth (internal/domain/errors.go),
#     surfaced on a voyager 401 as "authentication failed: re-run li-assist
#     auth login" (internal/voyager/client.go).
#   - "re-run li-assist auth login" -- the literal wrapper text on that
#     same 401 branch.
#   - \b401\b -- defensive only: no current caller prints the literal status
#     code, but if one ever does, this catches it for free.
AUTH_PATTERN = re.compile(
    r"not logged in|authentication fail(ed|ure)|re-run li-assist auth login|\b401\b",
    re.IGNORECASE,
)

# A 429 or a blown daily cap is a stronger "stop now" signal than a stale
# cookie: continuing only makes the block worse. Also grounded in the Go
# source:
#   - "rate limited" -- domain.ErrRateLimit (internal/domain/errors.go),
#     surfaced on a voyager 429 as "rate limited: voyager <path>".
#   - "daily cap exceeded" -- ratelimit.ErrDailyCapExceeded
#     (internal/ratelimit/limiter.go), surfaced as "daily cap exceeded: N
#     calls already made today (cap is 100)", itself wrapped by client.go's
#     local pre-flight check as "rate limit: daily cap exceeded: ...".
#   - \b429\b -- defensive only, same rationale as \b401\b above.
#   - "returned HTTP 40[13]" -- internal/voyager/client.go's `status >= 400`
#     fallthrough ("voyager <path> returned HTTP <n>: <snippet>"). 401 is
#     normally caught by its own dedicated case above and never reaches
#     this fallthrough in practice, but 403 does, and 403 is exactly a
#     LinkedIn challenge/checkpoint -- an account-flagged shape, treated
#     the same as a rate limit rather than an ordinary failure.
#   - \b999\b -- LinkedIn's own anti-bot response is HTTP 999, which also
#     falls through the same `status >= 400` branch as
#     "returned HTTP 999: <snippet>"; \b999\b is a defensive backstop in
#     case the wrapper wording ever changes but the numeral survives.
RATE_LIMIT_PATTERN = re.compile(
    r"rate limited|daily cap exceeded|\b429\b|\b999\b|returned HTTP 40[13]",
    re.IGNORECASE,
)


class ConfigError(Exception):
    """Unusable input or an unusable response. Reported to stderr and exits 2.

    Most raises happen before any network call (bad config, bad argument).
    cmd_show is the exception: it also raises this for a failed/unparseable/
    wrong-shape `jobs get` response, i.e. after the one call it makes. That's
    fine there because cmd_show has no fallback path of its own — unlike
    run_sweep, which raises RuntimeError instead so collect() can catch it,
    log a per-archetype failure, and keep sweeping the rest. cmd_show's
    failure has nowhere to go but straight to main(), which only catches
    ConfigError/AuthError — so it has to be one of those two, not a third
    type.
    """


class AuthError(Exception):
    """The LinkedIn session is dead (or dead enough to treat as such). Abort
    the whole run."""


class RateLimitError(AuthError):
    """LinkedIn is rate-limiting li-assist, or the daily cap is already
    spent. A subclass of AuthError so every existing `except AuthError` /
    `except (ConfigError, AuthError)` catches it too -- the abort behaviour
    is identical, only the message differs."""


@dataclass(frozen=True)
class Archetype:
    name: str
    label: str
    query: str
    pattern: re.Pattern
    location: str
    limit: int


@dataclass(frozen=True)
class Config:
    archetypes: tuple
    exclude_titles: tuple
    exclude_companies: tuple
    highlight_pattern: re.Pattern


def load_config(path) -> Config:
    """Parse and hard-validate the archetypes file. Raises ConfigError."""
    path = Path(path)
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (FileNotFoundError, IsADirectoryError, PermissionError):
        raise ConfigError(f"config not readable: {path}") from None
    except json.JSONDecodeError as exc:
        raise ConfigError(f"config is not valid JSON: {path} ({exc})") from None

    if not isinstance(raw, dict):
        raise ConfigError(f"config must be a JSON object: {path}")

    defaults = raw.get("defaults", {})
    if defaults is None:
        defaults = {}
    if not isinstance(defaults, dict):
        raise ConfigError(f"config 'defaults' must be a JSON object: {path}")
    entries = raw.get("archetypes") or []
    if not entries:
        raise ConfigError(f"config has no archetypes: {path}")

    archetypes = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            raise ConfigError(f"archetype[{index}] must be a JSON object")
        missing = [
            f for f in REQUIRED_FIELDS
            if not isinstance(entry.get(f), str) or not entry.get(f).strip()
        ]
        if missing:
            raise ConfigError(
                f"archetype[{index}] missing or empty required field(s): {', '.join(missing)}"
            )
        try:
            pattern = re.compile(entry["match"], re.IGNORECASE)
        except re.error as exc:
            raise ConfigError(
                f"archetype '{entry['name']}' has an invalid match regex: {exc}"
            ) from None
        raw_limit = entry.get("limit", defaults.get("limit", 25))
        try:
            limit = int(raw_limit)
        except (TypeError, ValueError):
            raise ConfigError(
                f"archetype '{entry['name']}' has an invalid limit: {raw_limit!r}"
            ) from None
        archetypes.append(
            Archetype(
                name=entry["name"],
                label=entry["label"],
                query=entry["query"],
                pattern=pattern,
                location=entry.get("location", defaults.get("location", "")),
                limit=limit,
            )
        )

    names = [a.name for a in archetypes]
    duplicates = sorted({n for n in names if names.count(n) > 1})
    if duplicates:
        raise ConfigError(f"duplicate archetype name(s): {', '.join(duplicates)}")

    # `highlight` names PLAIN terms, not regexes -- a user's differentiator
    # (e.g. "c++", ".net") must not be able to break the tool just because it
    # happens to contain a regex metacharacter. re.escape each term before
    # joining, so the compiled pattern only ever matches literally. Absent,
    # empty, or non-list `highlight` disables the feature cleanly rather than
    # raising -- it is optional operator context, not part of the archetype
    # contract the REQUIRED_FIELDS check above enforces.
    highlight_terms = defaults.get("highlight")
    highlight_pattern = None
    if isinstance(highlight_terms, list):
        escaped = [
            re.escape(term) for term in highlight_terms
            if isinstance(term, str) and term.strip()
        ]
        if escaped:
            highlight_pattern = re.compile("|".join(escaped), re.IGNORECASE)

    return Config(
        archetypes=tuple(archetypes),
        exclude_titles=tuple(defaults.get("exclude_title") or ()),
        exclude_companies=tuple(defaults.get("exclude_company") or ()),
        highlight_pattern=highlight_pattern,
    )


def job_link(urn: str) -> str:
    """urn:li:fsd_jobPosting:4431723620 -> the viewable posting URL."""
    return VIEW_URL.format(job_id=str(urn).rsplit(":", 1)[-1])


def cutoff_date(days: int, today=None) -> date:
    """Oldest date still considered in-window. `today` is injected for tests."""
    return (today or date.today()) - timedelta(days=days)


def bucket_of(posted_at, cutoff: date, last_run: date = None) -> str:
    """fresh | in | undated | old.

    LinkedIn sometimes returns no posting date at all, which arrives as the Go
    zero value. Those must land in their own bucket: a naive date filter would
    silently delete them.

    `last_run` defaults to None so every pre-existing call site keeps working
    unchanged. When it is set, a posting on or after it is "fresh" -- it
    genuinely appeared since the operator last looked, as opposed to merely
    being NEW because it churned out of `jobs sweep`'s cache-membership diff
    (see the module docstring's `NEW` caveat). Undated postings stay undated
    regardless of `last_run`: a missing date is not evidence of freshness.
    """
    if not posted_at or posted_at == ZERO_DATE:
        return "undated"
    try:
        posted = date.fromisoformat(str(posted_at)[:10])
    except ValueError:
        return "undated"
    if posted.year <= 1:
        return "undated"
    if last_run is not None and posted >= last_run:
        return "fresh"
    return "in" if posted >= cutoff else "old"


def labels_for(title: str, origin: str, archetypes) -> str:
    """Label a job against EVERY archetype, not just the sweep that found it.

    Sweep credits a job to whichever archetype ran first, which is ordering
    luck rather than meaning. Matching locally is free and naturally
    multi-label. Falls back to the originating label so the column is never
    blank when a title defeats all the patterns.
    """
    matched = [a.label for a in archetypes if a.pattern.search(title or "")]
    return ", ".join(matched) if matched else origin


def build_sweep_cmd(archetype: Archetype, config: Config) -> list:
    """The exact argv for one archetype's sweep. Never includes --enrich."""
    cmd = [
        "li-assist", "jobs", "sweep", archetype.query,
        "--limit", str(archetype.limit),
        "--format", "json",
    ]
    if archetype.location:
        cmd += ["--location", archetype.location]
    for title in config.exclude_titles:
        cmd += ["--exclude-title", title]
    for company in config.exclude_companies:
        cmd += ["--exclude-company", company]
    return cmd


def _invoke(cmd, run):
    return run(cmd, capture_output=True, text=True, check=False)


def _raise_if_abort_signal(stderr: str) -> None:
    """Shared triage for a nonzero exit's stderr: escalate the two known
    stop-now signals (rate limit checked first -- it is the stronger
    signal) before the caller falls through to an ordinary failure."""
    if RATE_LIMIT_PATTERN.search(stderr):
        raise RateLimitError(
            "LinkedIn is rate-limiting li-assist (or the daily cap is spent) — "
            "stop now and retry later; see `li-assist doctor`"
        )
    if AUTH_PATTERN.search(stderr):
        raise AuthError(
            "LinkedIn session is not usable — run 'li-assist auth login', then retry"
        )


def run_sweep(archetype: Archetype, config: Config, run=subprocess.run, log=None) -> list:
    """One sweep. Raises AuthError (or RateLimitError) on a dead session or
    a rate limit, RuntimeError otherwise."""
    if log is None:
        def log(message):
            print(message, file=sys.stderr)

    proc = _invoke(build_sweep_cmd(archetype, config), run)
    stderr = proc.stderr or ""
    if proc.returncode != 0:
        _raise_if_abort_signal(stderr)
        raise RuntimeError(f"exit {proc.returncode}: {stderr.strip()}")
    # A SUCCESSFUL sweep (exit 0) can still print a WARNING -- e.g.
    # li-assist could not open its job cache and ran without one, which
    # means this sweep cached nothing while still exiting 0. Left
    # unsurfaced, --seed would report success and write the marker while
    # having primed nothing, and every later run would silently report the
    # whole backlog as new, forever. Forward it rather than discarding it.
    for line in stderr.splitlines():
        if line.startswith("WARNING"):
            log(f"li-digest: {archetype.name}: {line}")
    try:
        data = json.loads(proc.stdout or "[]")
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"unparseable output: {exc}") from None
    if not isinstance(data, list):
        raise RuntimeError("expected a JSON array")
    for i, entry in enumerate(data):
        if not isinstance(entry, dict):
            raise RuntimeError(f"expected all elements to be objects; element {i} is {type(entry).__name__}")
    return data


def collect(config: Config, run=subprocess.run, log=None):
    """Sweep every archetype sequentially. Returns (rows, failed_names).

    NEVER parallelise this loop: a burst of automated requests is exactly what
    gets a LinkedIn account flagged.
    """
    if log is None:
        def log(message):
            print(message, file=sys.stderr)

    merged = {}
    failed = []
    for index, archetype in enumerate(config.archetypes):
        log(f"li-digest: sweeping {archetype.name}…")
        try:
            jobs = run_sweep(archetype, config, run, log)
        except AuthError:
            raise
        except RuntimeError as exc:
            log(f"li-digest: archetype '{archetype.name}' failed ({exc}) — continuing")
            failed.append(archetype.name)
            # No string list can be complete (see AUTH_PATTERN /
            # RATE_LIMIT_PATTERN above). A dead session or a hard block
            # fails every archetype identically, so two failures in a row
            # right at the start is itself worth treating as that signal --
            # even when neither stderr matched a known pattern. This is a
            # SHAPE check, not a wording check, so it catches whatever the
            # string patterns miss. It only ever looks at the first two
            # archetypes: one archetype failing must not lose the rest.
            if index == 1 and len(failed) == 2:
                raise AuthError(
                    "two archetypes failed in a row — stopping rather than "
                    "spending more calls; this is a guess based on "
                    "consecutive failures, not a confirmed dead session; "
                    "check `li-assist doctor`"
                ) from None
            continue
        for entry in jobs:
            urn = entry.get("urn")
            if not urn or urn in merged:
                continue  # first sighting wins
            merged[urn] = dict(entry, origin=archetype.label)
    return list(merged.values()), failed


BUCKET_HEADINGS = (
    ("fresh", "Posted since your last digest"),
    ("in", "In window"),
    ("undated", "Undated (LinkedIn gave no posting date)"),
    ("old", "Older than the window"),
)

TABLE_HEADERS = ("Posted", "Archetypes", "Company", "Title", "Location", "Link")


def enrich_rows(rows, config: Config, cutoff: date, last_run: date = None) -> list:
    """Add archetypes / link / bucket to every row, in one pass.

    li-assist's own output shape is trusted for the array-of-objects
    envelope (run_sweep already rejects anything else), but individual
    FIELD values inside an object are not guaranteed to be the type we
    expect -- a schema drift upstream could hand us `"title": 42` or
    `"company": "Acme"` (a bare string instead of {"name": ...}). Same
    defect class Task 6 fixed in cmd_show for `posting`/`company`: coerce
    rather than let a bad field type raise past main().

    `last_run` defaults to None, matching bucket_of, so a caller that has no
    last-run marker yet (first real run) gets today's behaviour unchanged.

    `highlight` is a plain boolean rather than the matched term(s): archetype
    matching already answers "which lane", `highlight` only answers "does
    this one deserve a second look" -- and a plain bool is what --json
    consumers and the star prefix both actually need.
    """
    return [
        dict(
            row,
            archetypes=labels_for(
                row.get("title") if isinstance(row.get("title"), str) else "",
                row.get("origin", ""),
                config.archetypes,
            ),
            link=job_link(row.get("urn", "")),
            bucket=bucket_of(row.get("posted_at"), cutoff, last_run),
            highlight=bool(
                config.highlight_pattern
                and config.highlight_pattern.search(
                    row.get("title") if isinstance(row.get("title"), str) else ""
                )
            ),
        )
        for row in rows
    ]


def _cells(row) -> tuple:
    company = row.get("company")
    company = company if isinstance(company, dict) else {}
    # Same defect class as the company/title guards above, one field
    # short: on Python 3.11+, date.fromisoformat accepts bare "YYYYMMDD",
    # so a non-string posted_at like the int 20260804 buckets as "in" in
    # bucket_of() (via str(posted_at)) and only crashes HERE, slicing the
    # original un-stringified value with [:10].
    posted_at = row.get("posted_at")
    posted_at = posted_at if isinstance(posted_at, str) else ""
    posted = "—" if row.get("bucket") == "undated" else (posted_at or "")[:10] or "—"
    title = row.get("title", "")
    # Widths are computed from these same cell strings (see render_table), so
    # the star participates in width calculation for free -- no separate
    # alignment logic needed.
    title = f"★ {title}" if row.get("highlight") else title
    return (
        posted,
        row.get("archetypes", ""),
        company.get("name", ""),
        title,
        row.get("location", ""),
        row.get("link", ""),
    )


def render_table(rows) -> str:
    """Four buckets, newest first, empty buckets suppressed."""
    blocks = []
    for bucket, heading in BUCKET_HEADINGS:
        subset = [r for r in rows if r.get("bucket") == bucket]
        if not subset:
            continue
        subset.sort(key=lambda r: r.get("posted_at") or "", reverse=True)
        grid = [TABLE_HEADERS] + [_cells(r) for r in subset]
        widths = [max(len(str(row[i])) for row in grid) for i in range(len(TABLE_HEADERS))]
        lines = [f"{heading} ({len(subset)})"]
        lines += [
            "  ".join(str(value).ljust(width) for value, width in zip(row, widths)).rstrip()
            for row in grid
        ]
        blocks.append("\n".join(lines))
    return "\n\n".join(blocks)


def seed_marker(config_path) -> Path:
    """Marker lives beside the config it seeded, so test configs stay isolated."""
    return Path(config_path).parent / ".digest-seeded"


def last_run_marker(config_path) -> Path:
    """Marker lives beside the config, mirroring seed_marker. Records the
    UTC timestamp of the last successful non-seed run, so bucket_of can tell
    "genuinely posted since I last looked" (reliable, from posted_at) apart
    from merely "not in the cache" (unreliable -- see the module docstring's
    NEW caveat)."""
    return Path(config_path).parent / ".digest-lastrun"


def read_last_run(path, today: date = None) -> date:
    """Tolerant reader for the last-run marker. Missing file, empty file,
    malformed content, and a timestamp in the future all degrade to None
    rather than raising -- a corrupted-into-the-future marker must not be
    trusted as a cutoff (it would silently disable the fresh bucket forever,
    which is a worse failure than just having no fresh bucket this once).

    The marker is always written in UTC (see main()), so the future-guard
    must compare against UTC too. Comparing against a LOCAL date() -- the
    original bug here -- misjudges an evening run at a negative UTC offset:
    a UTC-7 user at 17:00 local writes a UTC stamp dated tomorrow-LOCAL, so
    every later run that same evening reads its own just-written marker
    back, judges it "future", and silently disables the fresh bucket with
    no message. `today` is the same optional test seam the rest of the
    module already uses (cutoff_date, bucket_of); production leaves it None
    and compares against the real UTC date.
    """
    try:
        text = Path(path).read_text(encoding="utf-8").strip()
    except (FileNotFoundError, IsADirectoryError, PermissionError):
        return None
    if not text:
        return None
    try:
        stamp = datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError:
        return None
    stamp_date = stamp.date()
    if stamp_date > (today or datetime.now(timezone.utc).date()):
        return None
    return stamp_date


def _is_remote(row) -> bool:
    """True only when `location` carries an explicit "(Remote)" marker,
    case-insensitively. li-assist has no server-side workplace filter
    (LinkedIn moved it behind SDUI), but every row's `location` already
    carries a (Remote) / (Hybrid) / (On-site) marker, so this filters for
    free. A row with NO marker at all is not treated as remote -- absence of
    a marker is not confirmation, it is just unknown."""
    location = row.get("location")
    return isinstance(location, str) and "(remote)" in location.lower()


def select_archetypes(config: Config, only: str) -> Config:
    """Narrow the config to --only names. Raises ConfigError on an unknown or
    all-blank name (e.g. "--only ,," must not silently sweep nothing)."""
    wanted = [name.strip() for name in only.split(",") if name.strip()]
    if not wanted:
        raise ConfigError("--only must name at least one archetype")
    known = {a.name for a in config.archetypes}
    unknown = [name for name in wanted if name not in known]
    if unknown:
        raise ConfigError(f"unknown archetype(s): {', '.join(unknown)}")
    return replace(
        config, archetypes=tuple(a for a in config.archetypes if a.name in wanted)
    )


class _Parser(argparse.ArgumentParser):
    """ArgumentParser that routes --help output and usage/error text through
    injected streams, so main's return-code contract holds even on argparse's
    own exit paths.

    Default ArgumentParser.error() writes straight to the real sys.stderr and
    calls the real sys.exit(2) — bypassing any injected `err` stream and
    raising SystemExit, which `except (ConfigError, AuthError)` cannot catch
    (SystemExit derives from BaseException, not Exception). main() catches
    the SystemExit this class raises and converts it to a return value.
    """

    def __init__(self, *args, out=None, err=None, **kwargs):
        self._digest_out = out if out is not None else sys.stdout
        self._digest_err = err if err is not None else sys.stderr
        super().__init__(*args, **kwargs)

    def print_help(self, file=None):
        super().print_help(self._digest_out if file is None else file)

    def error(self, message):
        self.print_usage(self._digest_err)
        self._digest_err.write(f"{self.prog}: error: {message}\n")
        raise SystemExit(2)


def build_parser(out=None, err=None) -> argparse.ArgumentParser:
    parser = _Parser(
        out=out, err=err,
        prog="li-digest",
        description="Sweep every CV archetype in one run and print what is new.",
        epilog="Also: li-digest show <urn|id> for one posting's full description. "
               "Supported on macOS and Linux; Windows users need WSL.",
    )
    parser.add_argument("--json", action="store_true", dest="as_json",
                        help="emit the merged array instead of a table")
    parser.add_argument("--only", default="",
                        help="restrict to named archetypes (comma-separated)")
    parser.add_argument("--window", type=int, default=14,
                        help="in-window cutoff in days (default: 14)")
    parser.add_argument("--config", default=CONFIG_DEFAULT, type=Path,
                        help=f"archetypes file (default: {CONFIG_DEFAULT})")
    parser.add_argument("--seed", action="store_true",
                        help="prime the cache silently so the next run is a true delta")
    parser.add_argument("--remote", action="store_true",
                        help="keep only rows whose location contains '(Remote)' "
                             "(case-insensitive); rows with no workplace marker at "
                             "all are excluded, not assumed remote")
    return parser


def cmd_show(arg, run=subprocess.run, out=None) -> int:
    """One posting, in full.

    Uses `jobs get` WITHOUT --enrich: the description is already in the
    payload. --enrich is only the LLM layer on top, and would spend a model
    call on text we already have.
    """
    out = out if out is not None else sys.stdout
    if not arg:
        raise ConfigError("show needs a job urn or id")

    arg = str(arg)
    if arg.startswith("urn:li:fsd_jobPosting:"):
        tail = arg[len("urn:li:fsd_jobPosting:"):]
        if not re.fullmatch(r"[0-9]+", tail):
            raise ConfigError(
                f"expected a numeric job id after 'urn:li:fsd_jobPosting:', got {arg}"
            )
        urn = arg
    elif arg.startswith("urn:li:"):
        # A foreign URN type (e.g. fsd_company). Reject it here, before any
        # call is spent, rather than silently concatenating it onto the
        # jobPosting prefix and letting li-assist fail on the mangled string.
        raise ConfigError(f"expected a jobPosting urn or a bare id, got {arg}")
    elif re.fullmatch(r"[0-9]+", arg):
        urn = f"urn:li:fsd_jobPosting:{arg}"
    else:
        # The else-branch used to accept ANY non-"urn:li:" string as a bare
        # id, so `li-digest show --json` built and ran the argv
        # ["li-assist", "jobs", "get", "urn:li:fsd_jobPosting:--json", ...].
        raise ConfigError(f"expected a jobPosting urn or a numeric id, got {arg}")

    proc = _invoke(["li-assist", "jobs", "get", urn, "--format", "json"], run)
    stderr = proc.stderr or ""
    if proc.returncode != 0:
        _raise_if_abort_signal(stderr)
        raise ConfigError(f"jobs get failed for {urn}: {stderr.strip()}")

    try:
        job = json.loads(proc.stdout or "{}")
    except json.JSONDecodeError as exc:
        raise ConfigError(f"jobs get returned unparseable output: {exc}") from None

    # Same defect class Task 3 closed for sweep output: valid JSON of the wrong
    # SHAPE must not reach .get() and raise a bare AttributeError.
    if not isinstance(job, dict):
        raise ConfigError(
            f"jobs get returned a JSON {type(job).__name__}, expected an object"
        )

    posting = job.get("posting")
    posting = posting if isinstance(posting, dict) else {}
    company_obj = job.get("company")
    company = company_obj.get("name", "") if isinstance(company_obj, dict) else ""
    print(job.get("title", ""), file=out)
    print(f"{company} — {job.get('location', '')}", file=out)
    print(file=out)
    print(f"Apply:  {posting.get('apply_url') or 'n/a'}", file=out)
    print(f"View:   {job_link(urn)}", file=out)
    print(file=out)
    print(posting.get("description") or "(no description in payload)", file=out)
    return 0


def main(argv=None, run=subprocess.run, out=None, err=None, today=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    out = out if out is not None else sys.stdout
    err = err if err is not None else sys.stderr

    def log(message):
        print(message, file=err)

    try:
        if argv and argv[0] == "show":
            return cmd_show(argv[1] if len(argv) > 1 else "", run=run, out=out)

        try:
            args = build_parser(out=out, err=err).parse_args(argv)
        except SystemExit as exc:
            # argparse's own exit paths (--help -> 0, malformed/unknown args
            # -> 2 via _Parser.error above). Convert to a return so main
            # never lets SystemExit escape except under __main__.
            return exc.code if isinstance(exc.code, int) else 2

        if args.window < 0:
            raise ConfigError("--window must not be negative")

        config = load_config(args.config)
        if args.only:
            config = select_archetypes(config, args.only)

        marker = seed_marker(args.config)

        if args.seed:
            _, failed = collect(config, run=run, log=log)
            # Written regardless of `failed`: refusing to write it would
            # force a full re-seed of every archetype (burning more calls)
            # just because one lane failed.
            marker.parent.mkdir(parents=True, exist_ok=True)
            marker.write_text(
                datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), encoding="utf-8"
            )
            if failed:
                # A failed archetype's cache was never primed, so its whole
                # backlog would surface as "new" on the next real run --
                # the opposite of what a clean seed promises. Say so, and
                # give the exact command to prime just the failed lane(s)
                # rather than burning calls on a full re-seed.
                log(
                    f"li-digest: seeded, but {len(failed)} archetype(s) were not "
                    f"primed: {', '.join(failed)} — their next run will report "
                    f"the full backlog as new. Re-run: li-digest --seed --only "
                    f"{','.join(failed)}"
                )
            else:
                log("li-digest: seeded — the next run reports only genuinely new roles")
            return 1 if failed else 0

        if not marker.exists():
            raise ConfigError(
                "cache not primed — run 'li-digest --seed' once first, then rerun"
            )

        last_run = read_last_run(last_run_marker(args.config), today=today)
        rows, failed = collect(config, run=run, log=log)
        rows = enrich_rows(rows, config, cutoff_date(args.window, today), last_run)

        # Applied after enrichment, before rendering, to both table and
        # --json output. li-assist has no server-side workplace filter
        # (LinkedIn moved it to SDUI), but `location` already carries the
        # marker, so this is free. Tracked separately from the render
        # branch below so the empty-result message can tell "the filter
        # removed everything" apart from "there was nothing to begin with".
        pre_filter_count = len(rows)
        if args.remote:
            rows = [r for r in rows if _is_remote(r)]
        remote_filtered_to_empty = bool(args.remote and pre_filter_count and not rows)

        if args.as_json:
            # [] in --json mode, even on the common "nothing new" outcome,
            # so a downstream pipe (jq, python -c 'json.load(...)') always
            # sees well-formed JSON rather than an empty stdin.
            print(json.dumps(rows, indent=2), file=out)
        elif rows:
            print(render_table(rows), file=out)
        elif remote_filtered_to_empty:
            # Distinct from the "nothing new" message below: there WERE
            # results, --remote removed all of them. Saying "nothing new"
            # here would imply an empty search, not a filtered one.
            log(
                f"li-digest: --remote filter left nothing to show "
                f"({pre_filter_count} result(s) before filtering)"
            )
        else:
            # render_table([]) == "" -- printing that put a bare "\n" on
            # stdout with no explanation on stderr, indistinguishable from
            # a bug. "Nothing new" is the most common daily outcome, so say
            # so on stderr instead and leave stdout genuinely empty.
            plural = "" if args.window == 1 else "s"
            log(f"li-digest: nothing new in the last {args.window} day{plural}")

        # Written AFTER rendering, deliberately: a crash mid-render must not
        # advance the stamp, or the next run would silently believe today's
        # (unprinted) postings were already seen. `today` stands in for
        # datetime.now() when a test injects it, same seam cutoff_date uses.
        #
        # Only written on a CLEAN, FULL run: `failed` means at least one
        # archetype's diff for this run is unreliable, and `--only` means
        # whole lanes were never swept at all. Advancing a GLOBAL stamp on
        # either would misbucket that lane's next genuinely-new postings as
        # "in" rather than "fresh" once it (or the rest) actually runs. This
        # is the opposite of --seed, where writing through a partial
        # failure IS correct -- re-seeding costs real calls, so refusing
        # would force burning more of them on lanes that already primed
        # cleanly. There is no such cost here: the stamp just waits for a
        # clean run.
        if not failed and not args.only:
            lastrun_path = last_run_marker(args.config)
            stamp = (
                datetime(today.year, today.month, today.day, tzinfo=timezone.utc)
                if today else datetime.now(timezone.utc)
            )
            try:
                lastrun_path.parent.mkdir(parents=True, exist_ok=True)
                lastrun_path.write_text(
                    stamp.strftime("%Y-%m-%dT%H:%M:%SZ"), encoding="utf-8"
                )
            except OSError as exc:
                # A missing marker already degrades gracefully (no fresh
                # bucket next time) -- so report and move on rather than
                # letting an unwritable config dir turn an otherwise
                # successful run into an uncaught traceback AFTER the table
                # already printed, which would break the 0/1/2 contract.
                log(f"li-digest: could not update the last-run marker: {exc}")

        if failed:
            log(f"li-digest: {len(failed)} archetype(s) failed: {', '.join(failed)}")
            return 1
        return 0

    except (ConfigError, AuthError) as exc:
        log(f"li-digest: {exc}")
        return 2


if __name__ == "__main__":
    sys.exit(main())
