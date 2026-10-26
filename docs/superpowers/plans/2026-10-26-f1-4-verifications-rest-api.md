# F1.4 plan — Verifications in the Caduceus REST API

Spec: `docs/superpowers/specs/2026-10-26-f1-4-verifications-rest-api.md`.

- [x] Check the item's premises against the code; record them under the
      item's Notes.
- [x] Acceptance tests in `internal/server/server_test.go`: list (newest
      first, `?to=`), get and 404, expire (expired, 409, 404) and its
      `verifyMu`, reset clears verifications, wait for an email. RED.
- [x] `server.go`: the three routes, `failVerification`, wait accepts `@`.
      GREEN.
- [x] README: the endpoint table lists the new routes and the reset note.
- [x] typecheck, lint, gofmt; server and store tests by name.
- [x] Flip `roadmap/F1.4.md` to `status: done`.
