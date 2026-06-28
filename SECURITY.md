# Security Policy

## Supported versions

`li-assist` is pre-1.0 and ships from `main`. Security fixes land in the latest
release; please run a recent version before reporting an issue.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem. Use GitHub's
private vulnerability reporting instead:

- Go to the repository's **Security** tab → **Report a vulnerability**.

That keeps the details private until a fix is available. I'll acknowledge the
report and follow up there.

## Scope notes

A couple of things are specific to this tool and worth keeping in mind:

- **Account safety is the dominant concern.** `li-assist` drives a real
  signed-in LinkedIn session through a private internal API. The biggest "harm"
  it could do is get the *operator's own* LinkedIn account flagged or banned.
  That's why the tool is single-user, read-only, and rate-limited. Changes that
  add write actions, parallelise requests, or weaken the rate limiter increase
  that risk and won't be accepted (see `CONTRIBUTING.md`).
- **No secrets in the repo.** Live session data (the `li_at` cookie, the Chrome
  profile), API keys, and your `Connections.csv` export never belong in git.
  `gitleaks` and `detect-private-key` run as pre-commit hooks as a backstop.
- **Dependencies** are scanned in CI (`govulncheck` for known CVEs, `gosec` for
  SAST) and kept current by Dependabot.
