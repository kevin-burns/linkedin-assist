# Grader — did `li-assist` handle this?

A **routing** check, on a request that names no tool at all.

## Pass

The response shows `li-assist`'s digest behaviour: `li-digest`, the saved archetypes in
`~/.config/li-assist/archetypes.json`, a sweep across every lane, and the bucketed table
(posted since your last digest / in window / undated / older). Asking which archetypes are
configured, or checking the session first, also counts.

## Fail

- The response asks what a "job digest" is, or invents an unrelated procedure.
- The response goes to public job boards — that is `job-feeds`.
- Generic output showing none of the digest's specific moves.

## Why this case exists

`li-digest` and `li-report` carried 185 lines of skill body and **no trigger in the
description** until 2026-08-19 — a branch with no pointer never fires. This case is the
direct test of that fix. Before it, this prompt could not have routed here.
