# Routing ablation — 2026-08-19

`claude plugin eval` is still gated ("`plugin eval` is currently in early access", exit 1,
every invocation form including `init`). The six cases in this directory are authored
against its documented layout and **have not been run by it**; that layout is unverified.

What follows is a **proxy**: each prompt went to a fresh Sonnet agent alongside both
competing skill descriptions, asked to pick one or none, with no hint of the expected
answer. Arms are the li-assist description **before** (1110 chars) and **after** (765) the
2026-08-19 rewrite. job-feeds was held constant.

## Round 1 — the authored cases

| case | expected | OLD | NEW |
|---|---|---|---|
| linkedin-sweep | li-assist | li-assist | li-assist |
| digest-unnamed | li-assist | li-assist | li-assist |
| html-report-fork | li-assist | li-assist | li-assist |
| since-last-fork | *not* li-assist | job-feeds | job-feeds |
| boards-not-linkedin | *not* li-assist | job-feeds | job-feeds |
| negative-neither | none | none | none |

12/12 correct, **zero between-arm delta**. No regression — and no discrimination either.
Every prompt carries an explicit disambiguator ("not LinkedIn", "from my LinkedIn cache"),
which a competent model resolves without either description being well written. A suite
where both arms score full marks measures nothing about the descriptions.

## Round 2 — disambiguators stripped, 3 runs per arm

**`html-ambiguous`** — *"Build me an HTML report of the roles worth looking at — something
I can open offline on the train."* No source named.

| arm | r1 | r2 | r3 |
|---|---|---|---|
| OLD | none | none | none |
| NEW | li-assist | li-assist | li-assist |

Within-arm agreement 3/3 in both; between-arm agreement 0/3. One OLD run said outright that
"neither skill covers building a standalone offline HTML report from LinkedIn results" —
`li_report.py` already existed and shipped; the pointer made it invisible. **This is the
result that justifies the rewrite.**

**`since-ambiguous`** — *"What's new since I last checked?"*

| arm | r1 | r2 | r3 |
|---|---|---|---|
| OLD | li-assist | none | none |
| NEW | none | none | none |

The feared steal did not happen; the reverse did. The OLD description grabbed this 1/3, the
NEW one abstains unanimously. The new pointer is **more** conservative on a genuinely
ambiguous ask.

This case also supplies the **noise floor**: OLD splits 1/2 across identical inputs, so
within-arm variance is real — which is what makes the unanimous 3/3-vs-3/3 split on
`html-ambiguous` meaningful rather than luck.

## Control — was the `self-contained` tweak necessary? No.

`self-contained HTML report` was removed from li-assist's description because the identical
phrase sits in job-feeds'. The hypothesis: that shared phrase would pull an ambiguous report
request toward job-feeds.

Three arms against the ambiguous prompt, 3 runs each:

| description | li-assist | job-feeds | none |
|---|---|---|---|
| OLD (1110 chars) | 0/3 | **0/3** | 3/3 |
| UNTWEAKED — self-contained present (748) | 2/3 | **0/3** | 1/3 |
| NEW — self-contained removed (765) | 3/3 | **0/3** | 0/3 |

**job-feeds was never chosen — 0 of 9 runs.** The collision does not exist. The shared phrase
pulled nothing.

The 2/3 vs 3/3 gap between untweaked and tweaked is **within the noise floor**: the
`since-ambiguous` OLD arm splits 1/2 across identical inputs, so a one-run difference at n=3
is not a signal. On this evidence the tweak neither helped nor hurt.

It is kept anyway, on the single-source-of-truth principle — two competing pointers should
not share a literal phrase — but **not** on any measured routing benefit. The hypothesis
that motivated it was wrong, and the record should say so.

What the three arms *do* show, unambiguously, is the effect that matters: 0/3 under the old
description versus 2/3 and 3/3 under both rewrites. The branch was unreachable; the rewrite
made it reachable. The exact wording is noise.

## Method limits

- Round 1 is one run per arm; rounds 2 and the control have 3.
- 30 proxy dispatches in total.
- Sonnet, not the session model.
- A forced two-way choice with both descriptions in view. Real routing has ~46 installed
  skills competing and no "pick one" instruction.
- A proxy for `claude plugin eval`, not a substitute. Re-run the authored cases through the
  real harness when early access lands.
