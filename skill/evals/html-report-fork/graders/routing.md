# Grader — did `li-assist` handle this?

A **routing** check on a deliberate collision.

## Pass

The response shows `li-report`'s behaviour: rendering `~/.config/li-assist/cache/jobs.jsonl`
into one HTML file with CSS and JS inlined and no CDN reference, a `--window`, and an
absolute `--out` path whose location is reported back.

## Fail

- The response reaches for `job-feeds`' aggregator report instead. That skill renders its
  own "filterable self-contained HTML report" from public boards — but this request names
  the **LinkedIn cache**, which `job-feeds` does not touch.
- The response hand-writes an HTML file from scratch, ignoring `li-report` entirely.

## Why this case exists

The phrase "self-contained HTML report" appears in **both** skills' descriptions. It entered
`li-assist`'s description on 2026-08-19, when the report branch was given a trigger for the
first time — so this collision is newly introduced and has never been measured. If this case
fails, the fix is to drop "self-contained" from `li-assist`'s pointer: the body carries it,
and it is the collidable token.
