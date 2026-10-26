# F1.2 plan — Verify start

Spec: `docs/superpowers/specs/2026-10-26-f1-2-verify-start.md`.

- [x] Read Twilio's Verify docs (channels, 60200, 60203, resend, update) and
      record the URLs under the item's Notes.
- [x] Acceptance tests in `internal/server/server_test.go`: intercept table
      (renamed, verify row flipped), host-aware pass-through, absolute-form
      never reaching the API, start through the proxy plus wait, custom
      codes, service scoping, channels and validation, resend and send limit,
      parallel sends, fetch/update/errors on the base URL, approve, update
      of a non-pending one, account SID, Messages API exclusion. RED.
- [x] `proxy.go`: `intercepted` covers `verify.*`.
- [x] `server.go`: per-host provider muxes, `serveProvider`, `verifyMu`,
      preset OTP in `capture`.
- [x] `twilio.go`: `twilioError` status argument, `twilioNotFound`, Verify
      captures excluded from the Messages API.
- [x] `verify.go`: routes, handlers, rendering. GREEN.
- [x] README: Verify section replaces the "codes go to real phones" note.
- [x] typecheck, lint, gofmt; server and store tests by name.
- [x] Flip `roadmap/F1.2.md` to `status: done`.
