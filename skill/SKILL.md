---
name: li-assist
description: Use this skill to search, track, and inspect LinkedIn job postings from the command line with the read-only `li-assist` tool. Trigger it whenever the user wants to search LinkedIn for jobs by keyword or boolean query (optionally within a location); find or track which postings are new since they last checked (diff/sweep new vs. already-seen roles, count new vs. seen); save or set a default job-search location so they don't retype it each time; pull one posting's full description, genuinely-required skills, salary, seniority, or apply URL — including by `urn:li:fsd_jobPosting:` id; enrich job descriptions into structured insights with a local or API LLM; or set up a repeatable daily/morning job-watch they can rerun. Applies even when they don't name the tool and just say things like "search LinkedIn for…", "show me new roles near X", or "track new <role> jobs" — and consult it before scripting any LinkedIn job workflow so the read-only and rate-limit safety constraints are respected. This tool is read-only — NOT for posting, messaging, commenting, connecting, applying, or people/company lookup.
license: MIT
---

# li-assist

`li-assist` is a single Go binary that gives one person programmatic, **read-only**
access to LinkedIn for job hunting and research. It rides a real logged-in Chromium
session (via `auth login`) and issues LinkedIn's internal `voyager` requests in-page, so
results are the same data the website sees. Job payloads go to **stdout** as JSON; status
and audit lines go to **stderr**, so you can pipe stdout straight into context.

## The hard constraints — read these first

These are not style preferences. Violating them risks the user's real LinkedIn account,
which is **catastrophic and unrecoverable**. The tool is built to make them hard to break;
your job is to never try.

- **Read-only, always.** `li-assist` only reads. It never posts, comments, reacts, messages,
  connects, follows, or applies. If the user asks you to do any of those *through* this tool,
  it cannot — say so plainly rather than improvising a workaround.
- **Never bypass the rate limiter.** Every network verb goes through a jittered limiter with a
  daily cap. Do not parallelise calls, loop tightly over many URNs, or set the gap/cap env vars
  to defeat the throttle. A burst of automated requests is exactly what gets an account flagged.
  Prefer one `sweep` over many `get`s; let enrichment caps (`--enrich`) do their job.
- **Stay inside the supported surface.** Only `jobs` (search/get/sweep) is available. Company
  search/get/employees and person search/get are **deferred** — LinkedIn serves them via
  Server-Driven-UI (SDUI), not clean voyager, so the tool does not implement them. Don't claim
  or attempt them. **Distance/radius filtering is also deferred** — LinkedIn removed it from the
  clean voyager jobs endpoint (now SDUI/AI search only); `--radius` does not exist.

If a request needs something outside this surface, the honest answer is "li-assist doesn't do
that (by design)", not a hack.

## Make sure the binary is installed

As the **first step of any task** — before any `jobs`/`auth`/`doctor` call — run the bundled
bootstrap once. Just run it; you don't need permission to install the user's own tool, and you don't
need to pre-probe with `command -v` or hunt the filesystem first. The script is the single source of
truth and is idempotent — if `li-assist` is already on `$PATH` it does nothing in ~20ms.

```bash
bash "<this skill's dir>/scripts/ensure-installed.sh"
```

Act on its **last stdout line**:

- `READY <version>` → installed and runnable; proceed with the task.
- `MISSING <reason>` → install couldn't complete; read the stderr detail and see "If the bootstrap
  can't install" below.

The script tries the fastest path first and announces the one it takes in a single stderr line, e.g.
`li-assist not installed — downloading prebuilt v0.1.4 (darwin/arm64)…`. Surface that line to the user
so they know what happened, then continue. Its order is:

1. **already on `$PATH`** → nothing to do;
2. **download the prebuilt release binary** for this OS/arch from the latest GitHub release
   (`kevin-burns/linkedin-assist`) — no toolchain, a few seconds. This is the normal path;
3. **build from source** — only if the download can't be reached — preferring an existing local
   checkout (including the current directory) and cloning only if none is found (needs Go).

macOS Gatekeeper is a non-issue for both install paths: `curl`/`gh`/`go` never set the quarantine
attribute, so nothing is blocked. (Only a binary a human downloaded through a *browser* would need
`xattr -d com.apple.quarantine`.)

**If the bootstrap can't install** (`MISSING`, e.g. offline with no local checkout and no Go): tell the
user what the stderr said and offer the manual fallback — grab the matching
`li-assist_<version>_<os>_<arch>.tar.gz` from <https://github.com/kevin-burns/linkedin-assist/releases>,
extract, and put `li-assist` on `$PATH`. If the script warns its install dir isn't on `$PATH`, relay
that fix (add `~/.local/bin` or `$(go env GOPATH)/bin` to `$PATH`) rather than invoking the binary by
absolute path.

## First-time setup: log in

`li-assist` needs a logged-in browser session before any `jobs` call works.

```bash
li-assist auth login      # opens Chrome; log in to LinkedIn, then it captures the session
li-assist auth status     # shows captured-at, age, and a 14-day staleness verdict
```

The session is treated as **stale after 14 days** (`LI_ASSIST_REAUTH_DAYS`, default 14) even if
the cookie's nominal expiry is later — a deliberate safety stance. If `auth status` or a verb
reports "not logged in" or staleness, the fix is always `li-assist auth login` again. Run
`li-assist doctor` to diagnose: it checks credentials/staleness, login health, and makes **one**
rate-limited probe to distinguish a re-auth need from LinkedIn schema drift.

## Verbs and flags

### `jobs search <keyword...>` — search postings

Multiple positional args are joined with a space, so `search senior platform engineer` ==
`search "senior platform engineer"`. LinkedIn boolean operators pass through:
`search '"platform engineer" OR devops NOT recruiter'`.

| Flag | Meaning |
|---|---|
| `--location "Berlin"` | filter by location; overrides the config default (below) |
| `--anywhere` | search worldwide (no location); mutually exclusive with `--location` |
| `--limit 25` | max results (default 25) |
| `--exclude-company "Acme"` | drop results from this company (repeatable; also reads `~/.config/li-assist/excluded-companies.txt`) |
| `--exclude-title "staff"` | drop results whose title contains this term, case-insensitive (repeatable) |
| `--format json` | `json` (default) or `okf` (OKF is stubbed — returns a clear "not yet implemented" error) |

### `jobs get <urn>` — full detail for one posting

`<urn>` must be `urn:li:fsd_jobPosting:<id>`. Cache-first: a previously fetched URN returns from
`~/.config/li-assist/cache/jobs.jsonl` with no network call.

| Flag | Meaning |
|---|---|
| `--refresh` | force a re-fetch even if cached |
| `--enrich` | run LLM analysis of the description (see Enrichment); cached per URN (enrich-once) |
| `--format json` | `json` (default) or `okf` (stubbed) |

### `jobs sweep <keyword...>` — diff new postings since last run

Runs a search and compares against the local cache. Prints only **new** postings to stdout;
**always** prints an audit line to stderr (never silent):

```
sweep: 3 new / 12 seen / 0 excluded (cache: 240 jobs)
```

Accepts the same keyword/location/exclude flags as `search`, plus:

| Flag | Meaning |
|---|---|
| `--all` | print both new and seen results, not just new |
| `--enrich` | fetch full detail + enrich each **new** posting (enrich-once; capped, default 25 via `LI_ASSIST_ENRICH_MAX_PER_RUN`). Plain sweep does NO detail fetches. |
| `--format json` | json only for sweep |

When `--enrich` runs, the audit line gains a suffix, e.g.
`... | enriched 3/3 new (cap 25)` (with `, N error(s)` / `, N skipped` when relevant).

### `config location` — set a default search location

So the user doesn't retype `--location` every time. Stored in `~/.config/li-assist/config.json`.

```bash
li-assist config location "Aachen, Germany"   # set
li-assist config location                      # show
li-assist config location --clear              # clear
```

Resolution precedence on search/sweep: `--anywhere` > `--location X` > config default > (empty).

## JSON output shapes

`jobs search` / plain `jobs sweep` emit a JSON **array** of:

```json
{ "urn": "urn:li:fsd_jobPosting:123", "title": "...", "location": "...",
  "company": { "urn": "", "name": "Acme" }, "posted_at": "2026-06-10T00:00:00Z" }
```

`posted_at` is omitted when unknown. `company.urn` is often empty for search results.

`jobs get` (and `sweep --enrich`) emit the **detail** shape — same fields plus `posting` and an
optional `insights` block:

```json
{ "urn": "...", "title": "...", "location": "...", "company": { "urn": "...", "name": "..." },
  "posted_at": "...",
  "posting": { "description": "...", "apply_url": "...", "applicant_count": 0 },
  "insights": {
    "real_summary": "...", "top_skills": ["..."], "salary_range": "...",
    "seniority": "...", "condensed_description": "...", "notes": "..."
  } }
```

`insights` appears only with `--enrich`; `salary_range`, `seniority`, `notes` are omitted when empty.

## Enrichment (`--enrich`)

LLM analysis that de-markets a job description into structured `insights`: the real summary,
the genuinely *required* skills, salary (verbatim if stated), seniority, a condensed description,
and notes. Provider-agnostic, auto-detected in this order: **Ollama → OpenAI → Gemini → Anthropic**.

| Env var | Effect |
|---|---|
| `LI_ASSIST_ENRICH_PROVIDER` | force `ollama` / `openai` / `gemini` / `anthropic` (else auto-detect) |
| `LI_ASSIST_ENRICH_MODEL` | override the model for the chosen provider |
| `LI_ASSIST_ENRICH_MAX_PER_RUN` | cap on jobs enriched per `sweep --enrich` (default 25) |
| `OPENAI_API_KEY` / `GEMINI_API_KEY` / `ANTHROPIC_API_KEY` | enable the respective API provider |

Auto-detect uses Ollama if reachable, else the first API key present. With no provider, enrichment
is **skipped gracefully** (a note to stderr) and the command still succeeds — never treat a missing
provider as a failure. Ollama keeps everything local; if `LI_ASSIST_ENRICH_PROVIDER=ollama` and the
server is down, `li-assist` can auto-start it (`LI_ASSIST_OLLAMA_AUTOSTART=false` to disable).

## Rate-limit knobs (do not use these to go faster)

`LI_ASSIST_MIN_GAP_MS`, `LI_ASSIST_MAX_GAP_MS`, `LI_ASSIST_DAILY_CAP` tune the jittered cadence and
daily cap. They exist to let a cautious user go **slower / safer**, not to defeat throttling. Leave
defaults unless the user explicitly wants to be more conservative.

## Archetype digest (`li_digest.py`)

`skill/scripts/li_digest.py` sweeps several saved "archetype" searches in one run and prints
what is new, labelled by which archetypes each role matches. It exists because the daily
question is "what appeared across all my lanes", not "what matches one keyword".

Python 3 standard library only — no jq, no packages to install. **macOS and Linux only**,
matching what `li-assist` itself ships; Windows users need WSL.

Archetypes live in `~/.config/li-assist/archetypes.json` — **not** in the repo, because it is
personal data. `skill/scripts/archetypes.example.json` is the template. Set up once, from the
repo root:

```bash
mkdir -p ~/.config/li-assist ~/.local/bin
cp skill/scripts/archetypes.example.json ~/.config/li-assist/archetypes.json
ln -s "$PWD/skill/scripts/li_digest.py" ~/.local/bin/li-digest
```

The symlink is what makes `li-digest` a command. Without it, substitute
`python3 skill/scripts/li_digest.py` for `li-digest` in every example below.

```bash
li-digest --seed                 # once: prime the cache so the next run is a true delta
li-digest                        # the daily table
li-digest --json                 # pipeable JSON on stdout
li-digest --only em,platform     # one or two lanes
li-digest --window 30            # widen the lookback window in days (default: 14)
li-digest --config ~/tmp/test-archetypes.json  # point at an alternate archetypes file
li-digest --remote                # keep only rows marked (Remote); unmarked rows are excluded
li-digest show 4431723620        # full description for one posting
```

Exit codes: `0` clean, `1` one or more archetypes failed (the rest still printed), `2` config
or usage error — including a dead session, a rate limit or blown daily cap, or the
circuit breaker aborting after the first two archetypes fail in a row.

Every non-seed run that sweeps every archetype cleanly (no `--only`, no archetype failure) also
writes `.digest-lastrun` beside the config, and the next run uses it to add a `"Posted since your
last digest"` bucket ahead of the usual In window / Undated / Older buckets: any posting whose
`posted_at` is on or after that stamp is `fresh`, an honest answer to "did this genuinely appear
since I last looked" instead of the cache-membership-driven `NEW` from `jobs sweep` (see the
sweep/diff caveat in the main README). The comparison is date-granular and **inclusive**, so a job
posted the same calendar date as your last run still shows as fresh on every later run made that
same day — it is not "shows once, never again". A run with a failed archetype or narrowed with
`--only` does not advance the stamp, since it never looked at everything. The very first real run
(no marker yet) behaves exactly as before — no fresh bucket — and `--seed` never writes this
marker, since seeding suppresses output and would make the first real run show nothing as fresh.

Each archetype carries both a `query` (LinkedIn boolean, server-side) and a `match` (local
regex). The `match` is what produces multi-archetype labels — a role can be `Platform, EM`.
**When you change a `query`, review its `match`.** Drift between the two is the most likely
defect in this tool, and the test suite asserts each `match` fires on a plausible title.

The digest never passes `--enrich`: four archetypes with enrichment is four searches plus up
to 100 detail fetches, which is a bad daily habit against the rate limiter. Descriptions come
from `li-digest show` on the few roles that earn a closer look — `jobs get` already returns the
full description without any LLM.

Optional `defaults.highlight` in `archetypes.json` is a list of plain terms — **not regexes** —
that the operator knows are their own differentiators (e.g. `["terraform", "kubernetes"]`).
Archetypes only know CV titles, so a strongly-matching role can otherwise be invisible in a long
table; a matching title gets `"highlight": true` in `--json` and a `★ ` prefix in the table. Terms
are `re.escape`d before being joined into one case-insensitive pattern, so a term containing regex
metacharacters (`c++`, `.net`) is matched literally rather than breaking the tool. Absent, empty,
or non-list `highlight` disables the feature with no error. The `★` prefix is U+2605, an
ambiguous-width glyph in some terminal/font/CJK-locale combinations — the stdlib has no `wcwidth`,
so table alignment there may drift by a column in rare setups; this is a known, low-impact
cosmetic limitation, not a bug being tracked.

`li-assist` has no server-side workplace filter (LinkedIn moved it behind SDUI), but every row's
`location` already carries a `(Remote)`, `(Hybrid)`, or `(On-site)` marker, so `--remote` filters
locally, for free, applied after enrichment and before rendering — in both table and `--json`
output. It keeps only rows whose `location` contains `(Remote)` case-insensitively; **a row with
no marker at all is excluded, not assumed remote.** If `--remote` removes every row, the stderr
message says the filter emptied the result, not that nothing was found.

## Recipes

**Daily new-jobs sweep, local-LLM enriched:**
```bash
LI_ASSIST_ENRICH_PROVIDER=ollama \
  li-assist jobs sweep '"platform engineer" OR sre NOT recruiter' --location "Aachen, Germany" --enrich
# stdout: JSON array of NEW jobs with posting + insights; stderr: the audit line
```

**Pull one job's full detail and required skills:**
```bash
li-assist jobs get urn:li:fsd_jobPosting:4313223964 --enrich | jq '{title, skills: .insights.top_skills}'
```

**Exclude noise and cap results:**
```bash
li-assist jobs search devops --location Berlin --exclude-title recruiter --exclude-company "Staffing Co" --limit 10
```

## Errors and exit behaviour

- **"not logged in"** → run `li-assist auth login`. Don't retry blindly.
- **HTTP 401 on a probe** → session expired; re-login. **Schema-drift parse error** → the tool
  wraps it distinctly; `li-assist doctor` tells the two apart.
- A non-zero exit means the command failed; read the stderr message rather than assuming success.

## Provenance

`li-assist` is the user's own personal-use tool (repo
<https://github.com/kevin-burns/linkedin-assist>). It uses LinkedIn's private internal API via a
logged-in browser session; automated access is contrary to LinkedIn's terms, which is precisely why
it is single-user, read-only, and rate-limited. This skill drives that tool; it is not affiliated
with or endorsed by LinkedIn.
