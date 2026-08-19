---
name: li-assist
description: LinkedIn job hunting from the command line with the read-only `li-assist` binary and its `li-digest` / `li-report` companions. Search postings by keyword or boolean query and location; sweep for what is new since the last check; run the archetype digest across every saved search lane; render the cached postings as an offline HTML report; pull one posting's description, required skills, salary, seniority or apply URL by `urn:li:fsd_jobPosting:` id; set the default search location; enrich descriptions into structured insights with a local or API LLM. Fires on unnamed asks too — "search LinkedIn for…", "new roles near X", "my job digest". Reads job postings only — people and company lookup are out of scope, as are posting, messaging, connecting and applying.
license: MIT
---

# li-assist

`li-assist` is a single Go binary that gives one person programmatic, **read-only**
access to LinkedIn for job hunting and research. It rides a real logged-in Chromium
session (via `auth login`) and issues LinkedIn's internal `voyager` requests in-page, so
results are the same data the website sees. Job payloads go to **stdout** as JSON; status
and audit lines go to **stderr**, so you can pipe stdout straight into context.

Two Python siblings ship beside it and are documented below: `li-digest` sweeps every saved
archetype in one run, and `li-report` renders the cache as self-contained HTML.

## Guardrails — read these first

These protect the user's real LinkedIn account, where a mistake is unrecoverable. The tool
is built to hold them; your job is to work within them.

- **Read.** `li-assist` reads job postings. Asked to post, comment, react, message, connect,
  follow or apply, say plainly that the tool does not do it rather than improvising.
- **Let the limiter pace you.** Every network verb goes through a jittered limiter with a
  daily cap. Keep calls sequential and let the enrichment caps do their work; prefer one
  `sweep` over many `get`s. Bursts are what get an account flagged.
- **Stay on the supported surface: `jobs` (search/get/sweep).** Company and person
  search/get, and distance/radius filtering, are served by LinkedIn through Server-Driven-UI
  rather than clean voyager, so the tool leaves them alone. `--radius` does not exist.

When a request needs something outside this surface, "li-assist doesn't do that, by design"
is the honest answer.
## Install the binary

**First step of any task**, before any `jobs` / `auth` / `doctor` call. Just run it — this is
the user's own tool, and the script is idempotent: already on `$PATH` and it returns in ~20ms.
Skip the `command -v` probe.

```bash
bash "<this skill's dir>/scripts/ensure-installed.sh"
```

Act on the **last stdout line**:

- `READY <version>` → proceed with the task.
- `MISSING <reason>` → read the stderr detail, relay it, and offer the manual fallback: the
  matching `li-assist_<version>_<os>_<arch>.tar.gz` from
  <https://github.com/kevin-burns/linkedin-assist/releases>, extracted onto `$PATH`. When the
  script warns its install dir is off `$PATH`, relay that fix rather than calling the binary by
  absolute path.

The script announces which install path it took in one stderr line — surface that to the user,
then carry on.

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

## Rate-limit knobs — for going slower

`LI_ASSIST_MIN_GAP_MS`, `LI_ASSIST_MAX_GAP_MS` and `LI_ASSIST_DAILY_CAP` tune the jittered
cadence and daily cap. Leave the defaults; raise the gaps only when the user asks to be more
conservative than the tool already is.


## Archetype digest and HTML report → [`DIGEST.md`](DIGEST.md)

Two Python siblings ship beside the binary. Read [`DIGEST.md`](DIGEST.md) when the task is any
of: the multi-lane daily digest across saved search lanes (`li-digest`); writing or editing the
user's `~/.config/li-assist/archetypes.json`; a self-contained HTML job report (`li-report`);
or the `query`/`match` **drift** that archetype edits risk. It covers the archetypes file, the
fresh bucket, `--remote`, `highlight`, and where to put the report.

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

**Daily digest across every lane, then an HTML report:**
```bash
li-digest                        # table of what is new across all archetypes
li-report --out ~/job-search/prospects.html
```

**Exclude noise and cap results:**
```bash
li-assist jobs search devops --location Berlin --exclude-title recruiter --exclude-company "Staffing Co" --limit 10
```

## Errors and exit behaviour

- **"not logged in"** → run `li-assist auth login`. Re-login first, then retry.
- **HTTP 401 on a probe** → session expired; re-login. **Schema-drift parse error** → the tool
  wraps it distinctly; `li-assist doctor` tells the two apart.
- A non-zero exit means the command failed; read the stderr message rather than assuming success.

## Provenance

`li-assist` is the user's own personal-use tool (repo
<https://github.com/kevin-burns/linkedin-assist>). It uses LinkedIn's private internal API via a
logged-in browser session; automated access is contrary to LinkedIn's terms, which is precisely why
it is single-user, read-only, and rate-limited. This skill drives that tool; it is not affiliated
with or endorsed by LinkedIn.
