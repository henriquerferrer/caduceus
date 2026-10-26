# F1.3 plan — Verify check

Spec: `docs/superpowers/specs/2026-10-26-f1-3-verify-check.md`.

- [x] Read Twilio's VerificationCheck, Verify overview and error pages
      (60202, 60200, 20404); record the URLs under the item's Notes.
- [x] Acceptance tests in `internal/server/server_test.go`: check through
      the proxy (wrong, right, then 20404 on check and fetch), by
      `VerificationSid`, 60200 validation, attempt limit and 60202,
      canceled, service scoping, expired, parallel checks, base URL and
      pass-through on `api.twilio.com`. RED.
- [x] `verify.go`: `VerificationCheck` route, `verifyCheck`, the check
      resource, fetch of a non-pending verification answers 20404. GREEN.
- [x] README: the Verify section documents checking and its errors.
- [x] typecheck, lint, gofmt; server and store tests by name.
- [x] Flip `roadmap/F1.3.md` to `status: done`.
