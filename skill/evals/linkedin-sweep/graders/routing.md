# Grader — did `li-assist` handle this?

A **routing** check. Grade which skill's behaviour the response shows, not the quality of
the job advice.

## Pass

The response shows `li-assist`'s characteristic work: reaching for the `li-assist` binary,
`jobs sweep` (or the archetype digest) against the local cache to separate new from
already-seen, an audit line of the `N new / M seen` shape, and respect for the read-only
and rate-limit constraints. Checking the session with `auth status` or `doctor` first also
counts.

Namespacing is not the test — `li-assist` and any prefixed form are the same skill.

## Fail

- The response reaches for public job boards instead — that is `job-feeds`, and this
  request explicitly names LinkedIn, which `job-feeds` disclaims.
- The response is generic careers advice, or proposes scraping LinkedIn by hand. Generic
  is what the no-plugin baseline arm should look like, and that contrast is what makes the
  ablation meaningful.

## Why this case exists

The easy control. If this does not route, nothing else in the suite is interpretable.
