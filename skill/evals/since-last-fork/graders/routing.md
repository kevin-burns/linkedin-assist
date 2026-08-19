# Grader — did `li-assist` correctly stay out of the way?

An **inverse routing** check. Passing means `li-assist` did **not** fire.

## Pass

The response reaches for the public-board aggregator (`job-feeds`): the eight documented
JSON/RSS feeds, a deduplicated local database, remote and four-day-week filtering. Any
answer that does not invoke `li-assist` machinery passes this grader.

## Fail

- The response reaches for `li-assist`, `li-digest`, `jobs sweep` or the LinkedIn cache.
  The request explicitly excludes LinkedIn.

## Why this case exists

`li-assist`'s description gained "sweep for what is new since the last check" on
2026-08-19; `job-feeds` has long carried "what's new since I last looked". Near-identical
phrasing, two skills. This case measures whether the newer pointer steals traffic that
belongs to the older one — the risk runs in this direction, so a negative case is the only
way to see it.
