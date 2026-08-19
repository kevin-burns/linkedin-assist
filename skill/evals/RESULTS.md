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

## Control — was the `self-contained` tweak necessary?

`self-contained HTML report` was removed from the description before round 1, on the
grounds that the identical phrase sits in job-feeds' description. Re-running
`html-report-fork` against the **untweaked** description (self-contained present):

| r1 | r2 | r3 |
|---|---|---|
| li-assist | li-assist | li-assist |

**The collision did not reproduce.** The tweak was defensive and, on this evidence,
unnecessary. It is kept because two competing pointers sharing a literal phrase is a
single-source-of-truth problem independently of routing, and "offline HTML report" tested
clean on the hard case. **Caveat: the untweaked arm was only run against the explicit
prompt, not the ambiguous one — which is where a collision would be most likely to show.
That control is under-powered and the question is not fully settled.**

## Method limits

- Round 1 is one run per arm; only round 2 has replication.
- Sonnet, not the session model.
- A forced two-way choice with both descriptions in view. Real routing has ~46 installed
  skills competing and no "pick one" instruction.
- A proxy for `claude plugin eval`, not a substitute. Re-run the authored cases through the
  real harness when early access lands.
