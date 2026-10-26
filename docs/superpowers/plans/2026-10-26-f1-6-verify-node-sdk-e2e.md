# F1.6 plan — Stock twilio Node SDK drives Verify through the proxy

Spec: `docs/superpowers/specs/2026-10-26-f1-6-verify-node-sdk-e2e.md`.

- [x] Check the item's premises against the code; record them under the
      item's Notes.
- [x] `TestTwilioNodeSDKVerify` in `e2e/e2e_test.go`. RED: `verify.mjs` does
      not exist.
- [x] `e2e/twilio-node/verify.mjs` with `start` and `check`. GREEN.
- [x] Fail-not-skip: with `CI=true` and no `node_modules` the test fails;
      without `CI` it skips.
- [x] typecheck, lint, gofmt; the Node SDK e2e tests by name.
- [x] Flip `roadmap/F1.6.md` to `status: done`.
