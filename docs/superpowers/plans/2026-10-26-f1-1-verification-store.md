# F1.1 plan — verification store

Spec: `docs/superpowers/specs/2026-10-26-f1-1-verification-store.md`.

- [x] Write the acceptance tests in `internal/store/store_test.go` (round
      trip across reopen, pending lookup scoping, expiry on read with event,
      clear and pruning, events on writes, email matching, message channel
      fields, pre-F1 migration, email pending lookup). Run them: RED.
- [x] `internal/store/store.go`: schema, migration, `Message` fields,
      recipient normalisation, filter matching.
- [x] `internal/store/verification.go`: type, constants, methods, expiry.
- [x] Run the store tests: GREEN. Run the server tests (they use the store).
- [x] README: `-max-messages` also caps verifications.
- [x] typecheck, lint, gofmt.
- [x] Flip `roadmap/F1.1.md` to `status: done`.
