# F1.8 plan — Refuse cross-site and unknown-Host requests on the local listener

Spec: `docs/superpowers/specs/2026-10-27-f1-8-local-listener-guard.md`.

- [x] Check the item's premises against the code; record them under the
      item's Notes.
- [x] `Options.AllowedHosts` field (no behaviour yet), so the tests compile.
- [x] Acceptance tests in `internal/server/guard_test.go`: rebound Host on
      the code-bearing and write routes, cross-site writes, navigation
      exception, Host list, refusal shapes, proxy exemption. RED.
- [x] `internal/server/guard.go` and the `default:` branch of `ServeHTTP`.
      GREEN.
- [x] `cmd/caduceus/main.go`: `-allowed-hosts` / `CADUCEUS_ALLOWED_HOSTS`.
- [x] e2e: `TestAllowedHostsOption` in `e2e/e2e_test.go` (binary with and
      without `CADUCEUS_ALLOWED_HOSTS=caduceus`). RED, then GREEN.
- [x] README: Options row, Docker Compose note.
- [x] typecheck, lint, gofmt; server tests with `-race`; the e2e tests the
      change touches.
- [x] Flip `roadmap/F1.8.md` to `status: done`.
