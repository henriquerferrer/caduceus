# Scaffolded by sweap's /agentic-loop-init (copy-generalized from the
# agentic-ci convention). Single source of truth for "how to run X" — both
# locally and in CI. Recipe NAMES are the contract the pipeline (CLAUDE.md's
# sweap config, .github/workflows/*.yml) depends on — keep them stable. Recipe
# BODIES below are stack-specific stubs: replace every `# <fill in for your
# stack>` with your project's real command.

set shell := ["bash", "-uc"]

default: lint typecheck

lint:
    test -z "$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }
    go vet -tags e2e ./...

# Compiles every package and test file, including the e2e-tagged ones.
typecheck:
    go build ./...
    go test -tags e2e -count=1 -run '^$' ./... > /dev/null

# Unit + integration tests. A separate `e2e` recipe below covers end-to-end.
# Twice: the race detector slows every goroutine, so a test that only holds
# its timing under -race passes there and fails at full speed. The second
# pass runs at full speed in shuffled order.
test *args:
    go test -race -count=1 ./... {{ args }}
    go test -count=1 -shuffle=on ./... {{ args }}

# Builds the binary and drives it through the HTTPS proxy, including with the
# stock twilio Node SDK, and drives the inbox UI in headless Chromium. Needs
# node; installs both fixtures on first run, and Chromium when it is missing
# (with its system libraries when CI is set).
e2e *args:
    [ -d e2e/twilio-node/node_modules ] || npm ci --prefix e2e/twilio-node --no-audit --no-fund
    [ -d e2e/ui/node_modules ] || npm ci --prefix e2e/ui --no-audit --no-fund
    cd e2e/ui && npx --no-install playwright install --only-shell --no-remove $([ -n "${CI:-}" ] && echo --with-deps) chromium
    go test -tags e2e -count=1 ./e2e/ {{ args }}

# Meta recipes for agent ergonomics — keep these names; CI and the playbook
# depend on them. `ci-local` is the full gate: CLAUDE.md's sweap config sets
# `verify.e2e` (the command CI runs as the authoritative merge gate) to something
# that spans the whole gate, not just the `e2e` recipe — e.g. `just ci-local`, or
# `just test && just e2e`. Agents do NOT run it locally per item: they run what
# their change touched, and CI runs this.
ci-local:
    just lint
    just typecheck
    just test
    just e2e

ci-replay JOB:
    just {{ JOB }}

# Convenience wrappers around the sweap merge driver, for a human at a terminal.
# They are NOT the primary surface: the driver's own name is, because the sweap
# plugin puts its `bin/` on the Bash tool's PATH while it is enabled, so a
# session types `sweap-merge watch <n>` directly and the project allowlists
# `Bash(sweap-merge:*)` — one stable string, no path with a commit sha in it.
# The driver is NOT vendored into .github/scripts/ like audit-gate.sh: it
# resolves the merge gate and the post-merge hooks from its own directory, so
# it must run from the plugin dir beside its siblings. `bin/` gives it a stable
# name without moving the script out of the plugin.

# Merge a PR through the sweap merge driver: waits on the base branch's expected checks pinned to this head, runs the merge gate, merges, runs the post-merge hooks. One terminal word.
merge PR:
    @command -v sweap-merge >/dev/null 2>&1 || { echo "sweap-merge is not on PATH — the sweap plugin is not installed/enabled in this repo. Run: claude plugin update sweap@sweap --scope project, and restart the session."; exit 1; }
    @sweap-merge watch {{ PR }}

# Report what the merge driver is doing — every watched PR, or just one.
merge-status PR="":
    @command -v sweap-merge >/dev/null 2>&1 || { echo "sweap-merge is not on PATH — the sweap plugin is not installed/enabled in this repo. Run: claude plugin update sweap@sweap --scope project, and restart the session."; exit 1; }
    @sweap-merge status {{ PR }}

# Internal — invoked by ci.yml. Emits the agent-parseable JSON status block
# documented in CLAUDE.md's CI section. `.github/scripts/ci-status.sh` and
# `ci-summary.sh` are not shipped by the scaffold (they're stack-specific) —
# write them once for this project, or generate them via the `agentic-ci` skill.
_ci-status JOB STATUS:
    bash .github/scripts/ci-status.sh {{ JOB }} {{ STATUS }}

_ci-summary:
    bash .github/scripts/ci-summary.sh
