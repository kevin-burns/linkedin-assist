# Archetype digest and HTML report

The `li-digest` / `li-report` branch of the [li-assist skill](SKILL.md). Read this when the task
is the multi-lane daily digest, the archetypes file, or the HTML report — the core verbs
(`jobs search` / `get` / `sweep`) need none of it.

## The digest

`skill/scripts/li_digest.py` sweeps several saved "archetype" searches in one run and prints
what is new, labelled by which archetypes each role matches. It answers the daily question —
"what appeared across all my lanes" — where `jobs sweep` answers "what matches one keyword".

Python 3 standard library only. **macOS and Linux**, matching `li-assist` itself; Windows
needs WSL.

Archetypes live in `~/.config/li-assist/archetypes.json`, outside the repo, because they are
personal data. Link both commands once, from the repo root:

```bash
mkdir -p ~/.config/li-assist ~/.local/bin
ln -s "$PWD/skill/scripts/li_digest.py" ~/.local/bin/li-digest
ln -s "$PWD/skill/scripts/li_report.py" ~/.local/bin/li-report
```

Those symlinks are what make `li-digest` and `li-report` commands. Without them, substitute
`python3 skill/scripts/li_digest.py` and `python3 skill/scripts/li_report.py` throughout.

### Drift — the one property to protect

Every archetype carries a `query` and a `match`, expressing one intent in two languages:

- `query` is a LinkedIn boolean deciding which jobs are **fetched**, server-side.
- `match` is a local regex deciding which archetypes a title is **labelled** with, and is
  what produces multi-archetype labels — a role can be `Platform, EM`.

**Drift** is the gap that opens when one changes and the other does not: roles fetched that
nothing labels, or labels that silently narrow. Nothing in the tool checks the pair agree, it
is this project's most likely defect, and the test suite only asserts each `match` fires on a
plausible title — which drift survives.

So: derive both from one plain-English description, and re-derive both together on every
later edit. That single rule is why the next section has you write the file rather than hand
the user a template.

### Write the archetypes file for the user

`skill/scripts/archetypes.example.json` exists, and `li-digest` points at it when the file is
absent — but generating the file yourself is the better path, for two reasons specific to
this tool.

**Drift is generated away, not reviewed away.** Producing `query` and `match` together from
one description is the mitigation; a user editing raw JSON changes one and forgets the other.

**A guessed config costs real calls.** Each archetype is one `jobs sweep` per run against a
**100/day cap on the user's actual LinkedIn account** — four archetypes is four calls every
run, plus four more to `--seed`. Start with two or three and add more once the output earns it.

Ask what roles they want, their seniority, and their location, then write the file:

```json
{
  "defaults": { "location": "Germany", "limit": 50,
                "exclude_title": ["recruiter", "werkstudent"] },
  "archetypes": [
    { "name": "platform", "label": "Platform",
      "query": "\"platform engineer\" OR \"site reliability\" OR devops",
      "match": "platform engineer|site reliability|\\bsre\\b|\\bdevops\\b" }
  ],
  "highlight": ["terraform", "kubernetes"]
}
```

Every term the boolean can return has a counterpart in the regex — that is drift-free, and
the shape to preserve.

`match` is tested against the **title only**, so write title-shaped patterns: `platform
engineer`, not `kubernetes`. Tool terms belong in `highlight`, which stars a row and filters
nothing.

### First run

Check the session first, or the opening archetype fails and the circuit breaker aborts the rest:

```bash
li-assist auth status            # or: li-assist doctor
li-digest --seed                 # once: primes the cache so the next run is a true delta
```

Then the everyday commands:

```bash
li-digest                        # the daily table
li-digest --json                 # pipeable JSON on stdout
li-digest --only em,platform     # one or two lanes
li-digest --window 30            # lookback window in days (default: 14)
li-digest --remote               # keep only rows marked (Remote)
li-digest --config ~/tmp/test-archetypes.json
li-digest show 4431723620        # full description for one posting
```

Exit codes: `0` clean, `1` one or more archetypes failed and the rest still printed, `2`
config or usage error — including a dead session, a blown daily cap, or the circuit breaker
aborting after two consecutive archetype failures.

### What the table looks like

Buckets in order — fresh, in window, undated, older — with empty ones suppressed, newest first
inside each. Columns pad to content width; `★` marks a `highlight` hit; `—` is an unknown
posting date.

```
Posted since your last digest (2)
Posted      Archetypes    Company    Title                                Location                  Link
2026-08-18  Platform      Acme GmbH  ★ Senior Platform Engineer           Berlin, Germany (Remote)  https://www.linkedin.com/jobs/view/4431723620
2026-08-17  Platform, EM  Globex     Engineering Manager, Infrastructure  Munich (Hybrid)           https://www.linkedin.com/jobs/view/4429118844

In window (1)
Posted      Archetypes  Company  Title                Location         Link
2026-08-11  EM          Initech  Head of Engineering  Aachen, Germany  https://www.linkedin.com/jobs/view/4402881190
```

A role matching two archetypes shows both — `Platform, EM` above — which is the labelling the
`match` regexes exist to produce.

### The fresh bucket and `.digest-lastrun`

A run that sweeps every archetype cleanly — no `--only`, no failure, not `--seed` — stamps
`.digest-lastrun` beside the config. The next run then leads with a **"Posted since your last
digest"** bucket ahead of In window / Undated / Older: any posting dated on or after the stamp
is `fresh`. That answers "did this genuinely appear since I last looked", where `jobs sweep`'s
`NEW` only answers "is this absent from my cache".

The comparison is date-granular and inclusive, so a job posted on the same calendar date as
your last run stays fresh for the rest of that day. A narrowed or failed run leaves the stamp
alone, having never looked at everything.

Piping into something that closes the stream early — `| head`, quitting `less` — exits `0`
silently, and everything printed before that point is valid. The stamp deliberately does not
advance: you did not read every new posting, and advancing would demote the rows you never saw
to merely "in window" next time. Re-run without the pipe to get the stamp.

### Enrichment, highlight and `--remote`

The digest never passes `--enrich`. Four archetypes with enrichment is four searches plus up
to 100 detail fetches — the wrong daily habit against the limiter. Reach for descriptions on
the few roles that earn a closer look, via `li-digest show` or `jobs get`, both of which return
the full text with no LLM involved.

`defaults.highlight` is a list of plain terms — literal strings, `re.escape`d before joining —
naming the operator's own differentiators (`["terraform", "kubernetes"]`). Archetypes know CV
titles only, so a strongly-matching role can otherwise vanish in a long table; a hit gets
`"highlight": true` in `--json` and a `★ ` prefix in the table. Absent, empty or non-list
values disable the feature silently.

`--remote` filters locally, after enrichment and before rendering, in both table and `--json`.
LinkedIn moved its workplace filter behind SDUI, but every row's `location` still carries a
`(Remote)`, `(Hybrid)` or `(On-site)` marker. It keeps rows whose `location` contains
`(Remote)`, case-insensitively; **a row with no marker is excluded** rather than assumed
remote. When the filter empties the result, stderr says so, distinguishing that from finding
nothing.

### `li-report` — self-contained HTML export

`skill/scripts/li_report.py` is a sibling of the digest, not a merge into it: presentation
stays out of the 800-line digest script on purpose. It reads the same `archetypes.json` and
the li-assist job cache (`~/.config/li-assist/cache/jobs.jsonl`) and renders one HTML file
with CSS and JS inlined and no CDN reference, so it opens with **no network** — mail it, keep
it, read it on a train. It honours `exclude_company` against the cache, keeps in-window and
undated postings, and drops anything older than the window.

```bash
li-report                        # HTML to stdout — pipe or redirect
li-report --window 30 --out report.html
li-report --config ~/tmp/test-archetypes.json --generated-at "$(date -u +'%Y-%m-%d %H:%M UTC')" --out digest.html
```

`--window` (default 14) and `--config` behave as they do in `li-digest`. `--generated-at`
stamps a caller-supplied label instead of the renderer calling the clock, defaulting to the
current UTC time. `--out` given writes the file and reports its size to stderr; omitted, the
HTML goes to stdout so it pipes. Exit codes: `0` clean, `2` config or usage error — a negative
`--window`, a missing archetypes file, or a missing cache.
### Finish at the report, and put it somewhere findable

**Produce the report and show it rather than waiting to be asked.** The digest table is a
preview; the report is what the user works from, so stopping at the table costs them a second
prompt for something they were always going to want.

**Pass an absolute `--out` path and say where you put it.** A relative path resolves against
whatever the working directory happens to be, so the same command lands in a project folder one
day and `$HOME` the next — and a file the user has to hunt for is not a deliverable.

Keep every job-search artefact in one folder; `~/job-search` is a reasonable default:

```bash
li-report --window 30 --out ~/job-search/prospects.html
```

That co-location earns its keep when the user also aggregates public job boards: the boards lean
remote and contract, LinkedIn carries more permanent roles, so neither substitutes for the other
and the comparison only works with both reports side by side.
