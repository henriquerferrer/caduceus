# F1.5 plan — Verification badge in the inbox UI

Spec: `docs/superpowers/specs/2026-10-26-f1-5-verification-badge.md`.

- [x] Check the item's premises against the code; record them under the
      item's Notes.
- [x] Fixture `e2e/ui/` (package, lockfile, config, specs) and
      `TestInboxUI`; `just e2e` installs it and Chromium. Specs: lifecycle
      pending → approved and pending → expired with no reload, expiry with
      no event (page clock), no badge on a plain message, axe in light and
      dark. RED.
- [x] Fail-not-skip in CI: `TestInboxUI` with `CI=true` and no
      `node_modules` fails; without `CI` it skips.
- [x] `index.html`: verification map, badge, event handling, expiry timer;
      `--muted` contrast. GREEN.
- [x] ui-design Tier-1 loop: screenshots light/dark, desktop and 390px.
- [x] README: the Verify section mentions the badge.
- [x] typecheck, lint, gofmt; `just e2e`.
- [x] Flip `roadmap/F1.5.md` to `status: done`.
