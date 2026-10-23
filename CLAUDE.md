# Caduceus — agent instructions

Caduceus catches the SMS an application sends in development and tests, the way
Mailpit catches email. One Go binary (`cmd/caduceus`): SQLite store
(`internal/store`), HTTP server with the UI, REST API, emulated Twilio Messages
API and an HTTPS proxy mode that intercepts `api.twilio.com` with a local CA
(`internal/server`). See README.md for usage.

Invariants:
- Zero code changes for the user: official provider SDKs must work unmodified,
  configured only by environment (proxy + CA, or a base-URL env var where the
  SDK has one). `e2e/` proves this with the real twilio Node SDK.
- Proxy mode intercepts only the endpoints Caduceus emulates; everything else
  on a provider host is passed through to the real provider, and non-provider
  hosts are tunnelled untouched.
- Listens on 127.0.0.1 by default: it is a forward proxy and must not be open.
- Pure Go, no cgo (`modernc.org/sqlite`); the Docker image is `scratch`.

<!-- sweap:begin -->
**Read the sweap playbook's Run core (its first section, down to the
`run-core:end` marker) before any feature work:** the hard rules, the per-item
loop, the merge path and how to resume. The rest of the playbook is reference —
read a section when its situation arrives. Specs live in
`docs/superpowers/specs/`, plans in `docs/superpowers/plans/`. Backlog:
`ROADMAP.md` (index) + `roadmap/` (one file per item).

## sweap config (read by pipeline commands — override only what differs)
```
verify.typecheck: just typecheck
verify.lint:      just lint
verify.e2e:       just ci-local         # what CI runs as the authoritative merge gate: the FULL suite (unit+integration+e2e). Locally, run only the part your change touches.
backlog.file:     ROADMAP.md
backlog.dir:      roadmap/                  # one file per item — see playbook "Backlog format"
backlog.prefix:   F                        # one or more comma-separated prefixes, e.g. `F` or `F, DS`
# services.up:    just services-up          # uncomment only if you have local test services
# test.url:       http://localhost:3000     # /walkthrough only — where the acceptance pass opens the app; never production
# test.seed:      just seed-test            # /walkthrough only — the ONE command allowed to create test data
audit.lenses:     default
audit.baseline:   []
review.dimensions: security, ui
review.focus:     proxy safety (default bind stays loopback, so never an open proxy; only emulated routes are intercepted and everything else passes through untouched; the CA private key is never served), provider fidelity (responses and errors match the real provider's shapes so stock SDKs parse them), and the zero-code promise (no change may require users to edit application code)
# ui.contract:    docs/ui/surface-contract.md  # uncomment for UI-heavy projects — see ui-design skill §2
notify:           push
turns.default:    160
```

## Standing preferences — treat as already answered, do not re-ask
- Always work in an isolated worktree/branch; never implement on `main`.
- Finishing a branch: open a PR by default. Never discard work autonomously.
- Merging an unblocked PR whose gate is green is pre-authorized — do not ask.
  Never pass `--admin`/`--repo`/`-R`; never disable or weaken the gate.
- Plan execution: `/sweap:run` (the script holds the loop; continuous, no check-ins).
- TDD, no exceptions in product code. The implementer's report MUST include TDD
  Evidence: the RED (failing) output and the GREEN (passing) output, pasted.
  Never modify a test to make it pass; never skip/quarantine silently.
- Do not rationalize skipping a code review ("nothing to review", "trivial
  adapter"). Every task gets its fresh-context review.
- When blocked or ambiguous in an unattended run: escalate, don't guess —
  record the question in the item file's `blocked:` key (status: blocked),
  notify, move to the next item. NEVER create a `status: attended` item or
  transition one into it — hand-offs are the human's own planned work, authored
  only in `/roadmap`. Relabelling stuck work as human work is forbidden (and
  mechanically checked by the audit gate).
- Honor each backlog item's `review-dimensions` tags.
- At the end of an autonomous run: notify with merged-PRs + blocked-items +
  open-hand-offs (`status: attended`) summary.
<!-- sweap:end -->

<!-- agentic-ci: ci-section -->
## CI

CI uses GitHub Actions. Every job runs as `just <recipe>` — both locally and in
CI use the same commands. The `justfile` and `.github/workflows/*.yml` in this
repo are scaffolded from a template with `# <fill in for your stack>` stubs;
this section documents the convention those stubs must honor once filled in.

### Reproduce a CI failure locally

```bash
just ci-replay <job-name>   # e.g. just ci-replay test
just ci-local               # run the entire pipeline locally
```

### Read CI status from outside the runner

Each CI job emits a JSON status block to its step summary with shape:

```json
{
  "job": "<job-name>",
  "status": "success | failure | cancelled",
  "annotations": [{"file": "...", "line": 0, "level": "...", "message": "..."}],
  "repro": "just ci-replay <job-name>",
  "next_action": "..."
}
```

To read the status of a CI run without scraping logs:

```bash
gh run view <run-id> --json jobs
gh api repos/OWNER/REPO/actions/runs/<run-id>/jobs
```

The block is emitted by `.github/scripts/ci-status.sh <job> <status>` (per-job)
and aggregated by `.github/scripts/ci-summary.sh` (run-level). These two
scripts are stack-specific enough (they parse your lint/test tool's report
format) that the scaffold does not ship them — write them once for this
project (or generate them via the `agentic-ci` skill) to emit the shape above.

### Available `just` recipes

- `lint`, `typecheck`, `test`, `e2e` — individual checks (fill in per your stack)
- `ci-local` — run the full pipeline locally
- `ci-replay <job>` — re-run a single job

### Conventions

- All jobs run with `NO_COLOR=1`, `FORCE_COLOR=0`, `CI=true` to keep logs
  agent-parseable.
- Jobs run independently so a failure in one doesn't cancel the others —
  every gate reports its own result in the same pass.
- Third-party actions are pinned to 40-char SHAs; keep them updated on a
  schedule (e.g. Renovate/Dependabot).
<!-- /agentic-ci -->
