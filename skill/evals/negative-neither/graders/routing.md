# Grader — did `li-assist` correctly stay out of the way?

An **over-firing** check. Passing means no job-search tooling fired.

## Pass

The response engages with the career question directly — the trade-offs between an IC and a
management track. No tool invocation.

## Fail

- The response invokes `li-assist`, `li-digest`, `li-report` or a board aggregator. Nothing
  here asks for job listings; the words "platform engineer" are biography, not a search.

## Why this case exists

`li-assist`'s description names role vocabulary ("platform engineer" appears throughout its
body and recipes). A skill that fires on the mere presence of a job title over-fires. This
is the guard against a pointer that triggers too easily — the failure mode the description
rewrite could plausibly have introduced by broadening the trigger list.
