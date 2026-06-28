# Contributing to li-assist

Contributions are welcome — bug fixes, new job-research features, better docs,
more robust parsers. This is an MIT-licensed personal project; by contributing
you agree your work is released under the same [MIT License](LICENSE).

Before you write code, please read the guardrails below. They're not bureaucracy
— they're the reason the tool is safe to use at all.

## The guardrails (non-negotiable)

`li-assist` drives a real, signed-in LinkedIn session through LinkedIn's private
internal (`voyager`) API. The worst outcome it could cause is getting the user's
own LinkedIn account flagged or banned. Everything below exists to prevent that.

- **Read-only, always.** The tool reads; it never posts, comments, reacts,
  messages, connects, follows, or applies. There is no write path in the code,
  and adding one is an explicit non-goal. PRs that add write/engagement actions
  won't be merged.
- **Never weaken the rate limiter.** Every network call goes through a jittered
  limiter with a daily cap (`internal/ratelimit`). Don't bypass it, don't
  parallelise voyager calls, and don't add tight loops over many requests. A
  burst of automated requests is exactly what gets an account flagged.
- **Respect the deferred boundary.** Company search, people search, person
  profiles, post/article bodies, and the feed are deferred because LinkedIn
  serves them via Server-Driven UI (SDUI), not clean voyager — see the README's
  "What li-assist does — and deliberately does not — do". Don't add scrapers for
  those surfaces.
- **No live data in git, ever.** Never commit anything from `~/.config/li-assist/`
  (the `li_at` cookie, the Chrome profile), API keys, or a `Connections.csv`
  export. `gitleaks` + `detect-private-key` run as pre-commit hooks as a backstop.

If a change you want needs to cross one of these lines, open an issue first so we
can talk about whether there's a safe way to do it.

## Getting set up

Requirements: Go (see `go.mod`) and a local Google Chrome / Chromium at runtime.

```sh
git clone https://github.com/kevin-burns/linkedin-assist
cd linkedin-assist
go build -o li-assist ./cmd/li-assist
pre-commit install        # gofmt / vet / build, golangci-lint, gitleaks, detect-private-key
```

Day-to-day checks (these mirror what CI runs):

```sh
make check          # dependency-direction gate + lint + tests (fast feedback)
make test-race      # tests with the race detector
make security-scan  # gosec (SAST) + govulncheck (CVE database)
make ship-check     # the full pre-release gate
```

## How the code is laid out

5-layer Clean Architecture, with the dependency direction **enforced in CI**
(`make check-deps`): inner layers never import outer ones. `domain` imports
nothing internal; `usecase` imports only `domain`; `cache`/`output`/`enrich`/
`connections` import only `domain`; `voyager`/`auth`/`cmd` may import inward. If
you add a package, add its rule to `make check-deps`. The README's "Repo map"
has the full picture.

## Testing

- **Test-first.** Parsers are pinned against committed, PII-clean replay corpora
  under `internal/voyager/testdata/`; the enrichment client is tested against a
  mock HTTP server. Unit tests make **no live network calls** — keep it that way.
- If you change a parser, update or add a corpus fixture and a test that decodes
  it. If you touch the LLM client, extend the mock-server tests.

## Pull requests

1. Fork, branch from `main`.
2. Make the change with tests; run `make check` (and `make test-race` /
   `make security-scan` for anything non-trivial).
3. Open a PR. CI must be green — `test (go 1.26)` and `lint + security` are
   required to merge.
4. Keep commits readable; conventional-commit prefixes (`feat:`, `fix:`,
   `docs:`, `ci:`, `chore:`) are appreciated and feed the release changelog.

A short PR that does one thing well, with a test, is the easiest to review and
merge. Thanks for helping out.
