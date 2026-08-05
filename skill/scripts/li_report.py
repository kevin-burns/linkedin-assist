#!/usr/bin/env python3
"""li-report — render a self-contained HTML report from the li-assist cache.

A sibling of li_digest.py, not a merge into it: li_digest.py is already
800+ lines of sweep/bucket/CLI logic, and presentation (a filterable table
with a description drawer) is a different concern that grows independently.
Rather than duplicate config loading, windowing, or bucketing, this module
imports li_digest's pure helpers and only adds what li_digest does not have:
reading the on-disk job cache and rendering HTML from it.

Unlike li_digest, which calls out to `li-assist jobs sweep` for fresh data,
li-report reads what is ALREADY in ~/.config/li-assist/cache/jobs.jsonl --
no network call, no rate-limiter interaction, no li-assist invocation at
all. It is a read of yesterday's (or this morning's) results, not a new
sweep, which is exactly right for "let me look at what I've already
collected" rather than "check LinkedIn again right now".

The report is deliberately self-contained: CSS and JS are inlined, there is
no CDN reference, and it opens with no network -- strictly better for
something you keep, mail, or read offline. Job titles, company names,
locations and descriptions in the cache are untrusted input straight from
LinkedIn, so escaping is the single highest-risk part of this file: every
interpolation of cache-derived data goes through `_esc`, which wraps
html.escape(..., quote=True).

stdout is the report (or nothing, when --out is given); stderr is human.
Exit: 0 clean, 2 usage or config error.

Supported platforms: macOS and Linux, matching what li-assist and li_digest
ship. Windows users need WSL.

Standard library only -- no Jinja2, no third-party packages, no CDN links.
"""

from __future__ import annotations

import argparse
import html
import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

# li_report.py lives beside li_digest.py; resolve the real directory (not
# argv[0]'s, which may be a symlink such as ~/.local/bin/li-report) so the
# import works regardless of how this script was invoked.
sys.path.insert(0, str(Path(__file__).resolve().parent))
import li_digest  # noqa: E402

CACHE_DEFAULT = Path.home() / ".config" / "li-assist" / "cache" / "jobs.jsonl"

# Case-insensitive, matching li_digest._is_remote's own convention for the
# same `(Remote)` marker (see its --remote flag) -- an earlier version of
# this regex was case-sensitive and silently diverged from that, undercounting
# rows like "Berlin (remote)" that li-digest --remote correctly keeps. A
# canonical-casing map (below) keeps the label and the .m.Remote/.m.Hybrid/
# .m.Onsite CSS classes stable regardless of the source casing.
#
# Not promoted into li_digest itself: li_digest only ever needs a boolean
# "is this remote" (_is_remote), whereas this needs a 3-way Remote/Hybrid/
# On-site/unknown classification that li_digest has no other use for --
# adding it there would be report-presentation logic leaking into the
# digest module this file is explicitly a sibling of, not a merge into.
_WORKPLACE_RE = re.compile(r"\((remote|hybrid|on-site)\)", re.IGNORECASE)
_WORKPLACE_CANONICAL = {"remote": "Remote", "hybrid": "Hybrid", "on-site": "On-site"}


def _esc(value) -> str:
    """Every interpolation of cache-derived (LinkedIn) data goes through
    here. `value` may be None, a number, or any other JSON-decoded type --
    all are coerced to text before escaping."""
    return html.escape("" if value is None else str(value), quote=True)


def load_cache(path) -> list:
    """Read the li-assist job cache (JSONL: one JSON object per line).

    Raises ConfigError (li_digest's, reused rather than duplicated) if the
    file is missing/unreadable, or a line is malformed JSON or not an
    object -- the same "fail clearly before rendering garbage" stance
    load_config takes for the archetypes file. Blank lines are skipped.

    Catches OSError rather than naming FileNotFoundError/IsADirectoryError/
    PermissionError individually -- OSError is their common base and also
    catches NotADirectoryError (a path whose parent component is itself a
    regular file), which the narrower tuple missed. UnicodeDecodeError is
    caught separately: invalid UTF-8 in the cache is a real failure mode
    and is not an OSError at all.
    """
    path = Path(path)
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        raise li_digest.ConfigError(f"cache not readable: {path}") from None

    rows = []
    for lineno, line in enumerate(text.splitlines(), start=1):
        line = line.strip()
        if not line:
            continue
        try:
            entry = json.loads(line)
        except json.JSONDecodeError as exc:
            raise li_digest.ConfigError(
                f"cache line {lineno} is not valid JSON: {exc}"
            ) from None
        if not isinstance(entry, dict):
            raise li_digest.ConfigError(
                f"cache line {lineno} must be a JSON object, got {type(entry).__name__}"
            )
        rows.append(entry)
    return rows


def _company_name(row) -> str:
    company = row.get("company")
    company = company if isinstance(company, dict) else {}
    name = company.get("name")
    return name if isinstance(name, str) else ""


def _is_excluded(row, exclude_companies_lower) -> bool:
    """Case-insensitive substring match, both sides lowered. The cache
    predates config changes -- a company added to exclude_company after
    those rows were cached would otherwise still show up here."""
    if not exclude_companies_lower:
        return False
    name = _company_name(row).lower()
    return any(term in name for term in exclude_companies_lower)


def select_rows(raw_rows, config: "li_digest.Config", window: int, today=None) -> list:
    """Cache rows -> enriched, filtered, sorted report rows.

    Applies exclude_company, enriches via li_digest (archetypes/link/
    bucket), then keeps everything except the `old` bucket -- in-window and
    undated rows both stay, since a missing posting date is not evidence a
    job is stale. No `last_run` is passed through to enrich_rows: li-report
    has no last-run marker of its own, so nothing ever lands in `fresh`.

    `today` is the same optional test seam li_digest.cutoff_date already
    exposes -- production leaves it None (real date.today()); tests inject
    a fixed date so window boundaries do not depend on the day the suite
    happens to run.
    """
    exclude_lower = tuple(t.lower() for t in config.exclude_companies)
    filtered = [r for r in raw_rows if not _is_excluded(r, exclude_lower)]
    cutoff = li_digest.cutoff_date(window, today)
    enriched = li_digest.enrich_rows(filtered, config, cutoff)
    kept = [r for r in enriched if r.get("bucket") != "old"]
    kept.sort(
        key=lambda r: (
            len([a for a in (r.get("archetypes") or "").split(",") if a.strip()]),
            # str(...), not a bare `or ""`: bucket_of already coerces
            # posted_at with str() before parsing it (see li_digest's own
            # comment on this exact defect class in _cells), but the row's
            # posted_at field itself is left untouched by enrich_rows. A
            # cache with a non-string posted_at (e.g. int 20260804)
            # alongside any string-dated row would otherwise compare
            # int < str mid-sort and raise TypeError, escaping past
            # `except ConfigError` in main() to an uncaught traceback.
            str(r.get("posted_at") or ""),
        ),
        reverse=True,
    )
    return kept


def _workplace(location) -> tuple:
    """location -> (mode, place). mode is one of Remote/Hybrid/On-site/
    unknown (canonically cased regardless of the source casing); place has
    the marker stripped."""
    location = location if isinstance(location, str) else ""
    match = _WORKPLACE_RE.search(location)
    mode = _WORKPLACE_CANONICAL[match.group(1).lower()] if match else "unknown"
    place = _WORKPLACE_RE.sub("", location).strip()
    return mode, place


_URN_TAIL_UNSAFE_RE = re.compile(r"[^A-Za-z0-9_-]")


def _urn_tail(row) -> str:
    """The numeric job id, used ONLY to build this report's own internal
    #fragment ids/links (`id='j<tail>'`, `href='#j<tail>'`) -- not the same
    thing as li_digest.job_link's tail, which builds an external
    linkedin.com URL and is used as-is (already both html.escape'd AND
    single-quoted at its one call site, which is sufficient for a URL that
    is never parsed as HTML markup).

    _esc() alone is NOT sufficient here: html.escape(quote=True) escapes
    `& < > " '` but not space, `=`, `/` or backtick, and an id/href value
    that isn't quoted breaks out of the attribute at the first space --
    e.g. urn tail `1 onmouseover=alert(1) x` renders `id=j1 onmouseover=
    alert(1) x`, live JS in a file this tool tells people to keep and open
    offline. The call sites also single-quote the attribute now (defense
    in depth), but quoting alone still leaves a single embedded quote
    character as an escape hatch if a future edit ever drops the _esc()
    call -- constraining the character set at the source closes the whole
    class rather than one attack string.
    """
    urn = row.get("urn")
    urn = urn if isinstance(urn, str) else ""
    tail = urn.rsplit(":", 1)[-1]
    return _URN_TAIL_UNSAFE_RE.sub("", tail)


def _posted_cell(row) -> str:
    """Never fabricates a date for an undated row -- bucket_of already told
    us it is undated; showing anything else here would contradict it."""
    if row.get("bucket") == "undated":
        return ""
    posted_at = row.get("posted_at")
    posted_at = posted_at if isinstance(posted_at, str) else ""
    return posted_at[:10]


def render_html(rows, config: "li_digest.Config", window: int, generated_at: str) -> str:
    """Pure render: no clock calls, no I/O. `generated_at` is injected by
    the caller (main), exactly as li_digest's callers inject `today`."""
    lanes = sorted({
        a.strip() for r in rows for a in (r.get("archetypes") or "").split(",") if a.strip()
    })
    n_remote = sum(1 for r in rows if _workplace(r.get("location"))[0] == "Remote")
    n_multi = sum(1 for r in rows if "," in (r.get("archetypes") or ""))
    n_star = sum(1 for r in rows if r.get("highlight"))
    n_jd = sum(1 for r in rows if r.get("description"))
    n_employers = len({_company_name(r) for r in rows if _company_name(r)})

    out = [
        "<!doctype html><html lang=en><head><meta charset=utf-8>",
        "<meta name=viewport content='width=device-width,initial-scale=1'>",
        f"<title>Job prospects</title><style>{_CSS}</style></head><body>",
        "<h1>Job prospects</h1>",
        f"<div class=sub>Generated {_esc(generated_at)} &middot; {int(window)}-day window "
        "&middot; no heuristics, no enrichment</div>",
        "<div class=kpis>",
    ]
    for label, val in (
        ("Prospects", len(rows)), ("Multi-lane", n_multi), ("Remote", n_remote),
        ("Starred", n_star), ("With JD", n_jd), ("Employers", n_employers),
    ):
        out.append(f"<div class=kpi><span>{_esc(label)}</span><b>{val}</b></div>")
    out.append("</div>")

    out.append(
        "<div class=controls>"
        "<input type=text id=q placeholder='Filter role, company, location…'>"
        "<select id=lane><option value=''>All lanes</option>"
        + "".join(f"<option>{_esc(lane)}</option>" for lane in lanes)
        + "</select>"
        "<select id=mode><option value=''>Any workplace</option>"
        "<option>Remote</option><option>Hybrid</option><option>On-site</option>"
        "<option>unknown</option></select>"
        "<label><input type=checkbox id=multi>Multi-lane</label>"
        "<label><input type=checkbox id=star>&#9733; only</label>"
        "<label><input type=checkbox id=jd>Has JD</label>"
        "<span id=count></span></div>"
    )

    out.append(
        "<table id=t><thead><tr><th>Posted<th>Lanes<th>Company<th>Role<th>Where"
        "<th class=no-print>Link</tr></thead><tbody>"
    )
    for row in rows:
        mode, place = _workplace(row.get("location"))
        title = row.get("title") if isinstance(row.get("title"), str) else ""
        company = _company_name(row)
        lanes_here = [a.strip() for a in (row.get("archetypes") or "").split(",") if a.strip()]
        jid = _urn_tail(row)
        posted = _posted_cell(row)
        has_jd = bool(row.get("description"))
        # `and jid`: an empty sanitised tail (missing/malformed urn) means
        # no valid #fragment id exists to link to. Linking anyway would
        # point every such row at the same bare `#j` and, worse, every
        # <details> below would collide on the same empty id -- render the
        # plain (still escaped) title instead of a link to nowhere.
        title_cell = (
            f"<a href='#j{_esc(jid)}'>{_esc(title)}</a>" if has_jd and jid else _esc(title)
        )
        out.append(
            f"<tr data-lanes='{_esc(','.join(lanes_here))}' data-mode='{_esc(mode)}'"
            f" data-multi='{1 if len(lanes_here) > 1 else 0}'"
            f" data-star='{1 if row.get('highlight') else 0}'"
            f" data-jd='{1 if has_jd else 0}'"
            f" data-text='{_esc((title + ' ' + company + ' ' + place).lower())}'>"
            f"<td>{_esc(posted) or '&mdash;'}"
            f"<td>{''.join(f'<span class=lane>{_esc(a)}</span>' for a in lanes_here)}"
            f"<td>{_esc(company)}"
            f"<td>{'&#9733; ' if row.get('highlight') else ''}{title_cell}"
            f"<td><span class='m {_esc(mode.replace('-', ''))}'>{_esc(mode)}</span> {_esc(place)}"
            f"<td class=no-print><a href='{_esc(row.get('link'))}' target=_blank rel=noopener>view</a></tr>"
        )
    out.append("</tbody></table>")

    for row in rows:
        if not row.get("description"):
            continue
        jid = _urn_tail(row)
        # No id attribute at all when the tail is empty, rather than the
        # colliding `id='j'` every such row would otherwise share -- see
        # the matching guard on title_cell above.
        id_attr = f" id='j{_esc(jid)}'" if jid else ""
        out.append(
            f"<details{id_attr}><summary><b>{_esc(row.get('title'))}</b> &mdash; "
            f"{_esc(_company_name(row))}</summary>"
            f"<div class=sub>{_esc(row.get('urn'))}</div>"
            f"<div class=jd>{_esc(row.get('description'))}</div></details>"
        )

    out.append(
        "<footer>Source: ~/.config/li-assist/cache/jobs.jsonl (li-assist, read-only). "
        f"Archetypes: {_esc(', '.join(a.name for a in config.archetypes))}. "
        f"Excluded: {_esc(', '.join(config.exclude_companies) or 'none')}.<br>"
        "Workplace shows <em>unknown</em> where it genuinely is: <code>jobs get</code> drops the "
        "marker that <code>jobs sweep</code> carries and overwrites the cached row.<br>"
        "Self-contained &mdash; inline CSS/JS, no CDN, opens with no network.</footer>"
    )
    out.append(f"<script>{_JS}</script></body></html>")
    return "".join(out)


def build_parser(out=None, err=None) -> argparse.ArgumentParser:
    parser = li_digest._Parser(
        out=out, err=err,
        prog="li-report",
        description="Render a self-contained HTML report from the li-assist job cache.",
        epilog="Sibling of li-digest: reads what li-assist has already cached, no "
               "network call. Supported on macOS and Linux; Windows users need WSL.",
    )
    parser.add_argument("--window", type=int, default=14,
                        help="in-window cutoff in days (default: 14)")
    parser.add_argument("--config", default=li_digest.CONFIG_DEFAULT, type=Path,
                        help=f"archetypes file (default: {li_digest.CONFIG_DEFAULT})")
    parser.add_argument("--generated-at", default=None,
                        help="timestamp/label stamped on the report "
                             "(default: the current UTC time)")
    parser.add_argument("--out", default=None, type=Path,
                        help="write the HTML here instead of stdout")
    return parser


def main(argv=None, out=None, err=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    out = out if out is not None else sys.stdout
    err = err if err is not None else sys.stderr

    def log(message):
        print(message, file=err)

    try:
        try:
            args = build_parser(out=out, err=err).parse_args(argv)
        except SystemExit as exc:
            # argparse's own exit paths (--help -> 0, malformed/unknown args
            # -> 2 via li_digest._Parser.error). Converted to a return so
            # main never lets SystemExit escape except under __main__.
            return exc.code if isinstance(exc.code, int) else 2

        if args.window < 0:
            raise li_digest.ConfigError("--window must not be negative")

        config = li_digest.load_config(args.config)
        # CACHE_DEFAULT is read as a module global here, deliberately not
        # bound as a default argument value -- tests monkeypatch
        # li_report.CACHE_DEFAULT and need that reflected at call time.
        raw_rows = load_cache(CACHE_DEFAULT)
        rows = select_rows(raw_rows, config, args.window)

        generated_at = args.generated_at
        if not generated_at:
            generated_at = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")

        document = render_html(rows, config, args.window, generated_at)

        if args.out:
            out_path = Path(args.out)
            try:
                out_path.write_text(document, encoding="utf-8")
            except OSError as exc:
                # A mistyped --out path (missing parent dir, path is itself
                # a directory, no permission) is the likeliest error on
                # this flag. Left uncaught, write_text's OSError escapes
                # past `except ConfigError` below to an uncaught traceback
                # and exit 1 -- outside the documented 0/2 contract.
                raise li_digest.ConfigError(f"cannot write {out_path}: {exc}") from None
            # stat().st_size, not len(document): the document is UTF-8 and
            # can contain multi-byte characters (job descriptions are
            # free-text LinkedIn content), so character count and on-disk
            # byte count are not the same number.
            size = out_path.stat().st_size
            log(f"li-report: wrote {out_path} ({size:,} bytes, {len(rows)} row(s))")
        else:
            try:
                print(document, file=out)
            except BrokenPipeError:
                # `li-report | head` closes stdin early; that is normal
                # pipeline behaviour, not a failure. Redirect the real fd
                # to /dev/null so Python's own atexit flush doesn't raise a
                # second BrokenPipeError on the way out. `out` in tests is
                # an io.StringIO with no real fileno(); guard for that.
                try:
                    devnull = os.open(os.devnull, os.O_WRONLY)
                    os.dup2(devnull, sys.stdout.fileno())
                except (OSError, ValueError):
                    pass
                return 0
        return 0

    except li_digest.ConfigError as exc:
        log(f"li-report: {exc}")
        return 2


# ---------------------------------------------------------------------------
# Presentation constants (CSS / JS). Fenced off from the logic above on
# purpose -- this is what a browser executes, not Python control flow, and
# there is no reason to read it before the rendering logic that uses it.
# Referenced by render_html() above; only evaluated when that function runs,
# by which point the whole module (including these two names) is loaded --
# EXCEPT when this file itself is run as __main__, which executes top to
# bottom, so the `if __name__ == "__main__"` guard must live below these two
# constants, not above them (an import as a plain module doesn't have this
# ordering hazard: `import li_report` never runs the __main__ block at all).
# ---------------------------------------------------------------------------

_CSS = """
:root{--bg:#f6f7f9;--fg:#212529;--mut:#6c757d;--line:#dee2e6;--card:#fff}
@media(prefers-color-scheme:dark){:root{--bg:#16181c;--fg:#e9ecef;--mut:#9aa0a6;--line:#343a40;--card:#1f2226}}
*{box-sizing:border-box}
body{margin:0;padding:1.5rem;background:var(--bg);color:var(--fg);
 font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
h1{font-size:1.5rem;margin:0 0 .25rem}
.sub{color:var(--mut);font-size:.85rem;margin-bottom:1rem}
.kpis{display:flex;flex-wrap:wrap;gap:.75rem;margin-bottom:1rem}
.kpi{background:var(--card);border:1px solid var(--line);border-radius:.5rem;padding:.5rem .9rem;min-width:7rem}
.kpi b{display:block;font-size:1.5rem;font-weight:600}
.kpi span{font-size:.68rem;text-transform:uppercase;color:var(--mut);letter-spacing:.03em}
.controls{position:sticky;top:0;background:var(--bg);padding:.6rem 0;display:flex;flex-wrap:wrap;
 gap:.5rem;align-items:center;border-bottom:1px solid var(--line);margin-bottom:.5rem;z-index:5}
input,select{background:var(--card);color:var(--fg);border:1px solid var(--line);
 border-radius:.375rem;padding:.3rem .5rem;font-size:.85rem}
input[type=text]{min-width:16rem}
label{font-size:.8rem;color:var(--mut);display:inline-flex;gap:.25rem;align-items:center}
#count{margin-left:auto;font-size:.8rem;color:var(--mut)}
table{width:100%;border-collapse:collapse;background:var(--card);border:1px solid var(--line);border-radius:.5rem}
th,td{padding:.35rem .6rem;text-align:left;font-size:.82rem;border-bottom:1px solid var(--line);vertical-align:top}
th{position:sticky;top:3rem;background:var(--card);font-size:.72rem;text-transform:uppercase;color:var(--mut)}
tr.hide{display:none}
tr:hover td{background:rgba(127,127,127,.07)}
.lane{display:inline-block;background:var(--fg);color:var(--bg);border-radius:.25rem;
 padding:0 .3rem;font-size:.66rem;margin-right:.15rem}
.m{border-radius:.25rem;padding:0 .3rem;font-size:.66rem;color:#fff}
.m.Remote{background:#198754}.m.Hybrid{background:#0d6efd}
.m.Onsite{background:#6c757d}.m.unknown{background:#adb5bd}
a{color:inherit}
details{background:var(--card);border:1px solid var(--line);border-radius:.5rem;padding:.75rem;margin:.5rem 0}
.jd{white-space:pre-wrap;font-size:.85rem;max-height:26rem;overflow-y:auto;margin-top:.5rem}
footer{margin-top:2rem;padding-top:1rem;border-top:1px solid var(--line);font-size:.75rem;color:var(--mut)}
@media print{.controls,.no-print{display:none}.jd{max-height:none}}
"""

_JS = """
const rows=[...document.querySelectorAll('#t tbody tr')];
const el=i=>document.getElementById(i);
function apply(){
  const q=el('q').value.trim().toLowerCase(),l=el('lane').value,m=el('mode').value;
  let n=0;
  for(const r of rows){
    const ok=(!q||r.dataset.text.includes(q))
      &&(!l||r.dataset.lanes.split(',').includes(l))
      &&(!m||r.dataset.mode===m)
      &&(!el('multi').checked||r.dataset.multi==='1')
      &&(!el('star').checked||r.dataset.star==='1')
      &&(!el('jd').checked||r.dataset.jd==='1');
    r.classList.toggle('hide',!ok); if(ok)n++;
  }
  el('count').textContent=n+' of '+rows.length;
}
['q','lane','mode','multi','star','jd'].forEach(i=>el(i).addEventListener('input',apply));
apply();
"""


if __name__ == "__main__":
    sys.exit(main())
