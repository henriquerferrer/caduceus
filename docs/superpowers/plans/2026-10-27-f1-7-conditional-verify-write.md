# F1.7 plan — A Verify write never overwrites a stored expiry

Spec: `docs/superpowers/specs/2026-10-27-f1-7-conditional-verify-write.md`.

- [x] Check the item's premises against the code; record them under the
      item's Notes.
- [x] Acceptance tests: `internal/store/store_test.go` (conditional update
      and resend against a stored `expired`), `internal/server/server_test.go`
      (the six handler cases, paused between read and write). RED.
- [x] Store: `ErrStale`, `SetClock`, `UpdateVerification(v, from)`,
      conditional resend in `SendVerification`. Existing call sites pass the
      status they read.
- [x] Server: check, update, start and the expire hook go round again from a
      fresh read on `ErrStale`. GREEN.
- [x] typecheck, lint, gofmt; server and store tests by name, with `-race`.
- [x] Flip `roadmap/F1.7.md` to `status: done`.
