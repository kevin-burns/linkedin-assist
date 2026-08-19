# Grader — did `li-assist` correctly stay out of the way?

An **inverse routing** check. Passing means `li-assist` did **not** fire.

## Pass

The response reaches for the public-board aggregator, or otherwise answers without
`li-assist`. Naming the boards the request names is enough.

## Fail

- The response reaches for `li-assist` or tries a LinkedIn search. The request names public
  boards and states there is no LinkedIn session, so any LinkedIn call would also fail at
  the auth gate.

## Why this case exists

The clean negative. `li-assist` and `job-feeds` share sixteen terms of four or more
characters, including "postings", "search", "roles" and "report". This checks that shared
vocabulary alone does not pull a boards request into the LinkedIn skill.
