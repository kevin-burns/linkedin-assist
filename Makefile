.PHONY: build test test-race lint check check-deps deps-check security-scan ship-check release clean

BINARY := li-assist
GO ?= go

build:
	$(GO) build -o $(BINARY) ./cmd/li-assist

test:
	$(GO) test ./...

# Run tests with the race detector. ~2x slower but catches data races that
# only surface under concurrent load.
test-race:
	$(GO) test -race ./...

# Lint with golangci-lint (gosec + errcheck + govet + staticcheck via .golangci.yml).
lint:
	golangci-lint run ./...

# Architecture gate: enforces the layered dependency direction.
# domain has no deps on other internal packages; usecase only imports domain;
# output only imports domain. Violations fail CI immediately.
check-deps:
	@echo "Checking dependency direction..."
	@! grep -rE "\"github\.com/kevin-burns/linkedin-assist/internal/(voyager|auth|cmd|output|ratelimit|logging|usecase)\"" internal/domain/ 2>/dev/null \
		|| (echo "FAIL: internal/domain/ must not import other internal packages" && exit 1)
	@! grep -rE "\"github\.com/kevin-burns/linkedin-assist/internal/(voyager|auth|cmd|output|ratelimit|logging)\"" internal/usecase/ 2>/dev/null \
		|| (echo "FAIL: internal/usecase/ must only import internal/domain/" && exit 1)
	@! grep -rE "fmt\.Println|fmt\.Print|fmt\.Fprintf|os\.Stdout|json\.Marshal" internal/usecase/ 2>/dev/null \
		|| (echo "FAIL: internal/usecase/ must not emit output" && exit 1)
	@! grep -rE "\"github\.com/kevin-burns/linkedin-assist/internal/(voyager|auth|cmd|output|ratelimit|logging|usecase)\"" internal/cache/ 2>/dev/null \
		|| (echo "FAIL: internal/cache/ must only import internal/domain/" && exit 1)
	@! grep -rE "\"github\.com/kevin-burns/linkedin-assist/internal/(voyager|auth|cmd|usecase|ratelimit|logging)\"" internal/output/ 2>/dev/null \
		|| (echo "FAIL: internal/output/ must only import internal/domain/" && exit 1)
	@! grep -rE "\"github\.com/kevin-burns/linkedin-assist/internal/(voyager|auth|cmd|cache|output|ratelimit|logging|usecase)\"" internal/enrich/ 2>/dev/null \
		|| (echo "FAIL: internal/enrich/ must only import internal/domain/ and stdlib" && exit 1)
	@! grep -rE "\"github\.com/kevin-burns/linkedin-assist/internal/(voyager|auth|cmd|cache|output|ratelimit|logging|usecase)\"" internal/connections/ 2>/dev/null \
		|| (echo "FAIL: internal/connections/ must only import internal/domain/ and stdlib" && exit 1)
	@echo "OK"

# Show outdated direct + transitive Go modules. Informational: exits 0 even
# when modules are stale, so it can run in a daily-driver loop without
# blocking. Use `make ship-check` for a release gate.
deps-check:
	@echo "== Outdated Go modules =="
	@$(GO) list -u -m all 2>/dev/null | grep -E '\[[^]]+\]' || echo "(all dependencies up to date)"

# SAST (gosec) + dependency CVE scan (govulncheck). Both fetched via
# `go run`, cached in $$GOPATH/pkg/mod after first run. Exits non-zero
# on findings so this can gate releases via `make ship-check`.
#
# gosec excludes (must match .golangci.yml gosec.excludes):
#   G104 -- "Errors unhandled" overlaps with errcheck (in golangci-lint);
#            keeping both produces duplicate reports.
#   G304 -- file paths from our own config constants (credentials.json,
#            usage.json, excluded-companies.txt), not user input.
security-scan:
	@echo "== gosec (static security analysis) =="
	$(GO) run github.com/securego/gosec/v2/cmd/gosec@latest -quiet -exclude=G104,G304 ./...
	@echo "== govulncheck (known CVEs in deps + stdlib) =="
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Pre-commit checks (fast feedback for daily iteration).
check: check-deps lint test

# Pre-release gate (slower, includes security scan + dep-currency report).
# Run before `goreleaser release`.
ship-check: check-deps lint test security-scan deps-check

release:
	goreleaser release --clean

clean:
	rm -f $(BINARY)
	rm -rf dist/
