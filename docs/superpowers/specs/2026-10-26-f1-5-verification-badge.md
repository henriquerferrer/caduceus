# F1.5 — Verification badge in the inbox UI (design)

Item: `roadmap/F1.5.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## Badge (`internal/server/ui/index.html`)

- A message with a `verification_sid` gets a `.verification` badge in its
  meta row, after the provider line. Its text is `<channel> · <status>`,
  for example `sms · pending`. The status is the verification's own value
  (`pending`, `approved`, `canceled`, `max_attempts_reached`, `expired`), so
  the status is conveyed as text; colour only repeats it (accent for
  `approved`, the error colour for the other non-pending statuses).
- A message whose verification is not known to the page (pruned, or the
  list read failed) shows the channel alone. A message with no
  `verification_sid` has no badge.
- The page keeps verifications in a map by sid. `load()` reads
  `GET /api/v1/verifications` next to `GET /api/v1/messages`; a failed read
  keeps the map it had. A `verification` event on `/api/v1/events` replaces
  that sid's entry and re-renders without refetching; other events reload,
  as before. Between a list read and an event, the copy with the later
  `updated_at` wins.
- Expiry with no event: a pending verification whose `expires_at` is not
  after the browser's clock is shown as `expired`. After each render a
  single timer re-renders at the soonest future `expires_at` among the open
  thread's pending verifications.

## Contrast fix

The axe scan the item requires found `color-contrast` (serious) violations
that predate this item: `--muted` text on the outbound bubble (`--out`) was
4.05:1 in both themes. `--muted` moves to `#69675f` (light) and `#a6a49d`
(dark), at least 4.5:1 on every background the page uses. No other style
changes.

## Browser fixture (`e2e/ui/`)

- Its own `package.json` and lockfile pinning `@playwright/test` 1.64.1 and
  `@axe-core/playwright` 4.14.0; `playwright.config.mjs` (one worker,
  Chromium, `baseURL` from `CADUCEUS_URL`); `inbox.spec.mjs`.
- `TestInboxUI` in `e2e/e2e_test.go` starts the built binary and runs
  `npx --no-install playwright test` in `e2e/ui` with `CADUCEUS_URL` set.
  With `CI` set and the fixture not installed it fails; otherwise it skips,
  as `TestTwilioNodeSDK` does.
- `just e2e` installs the fixture with `npm ci` when `node_modules` is
  missing, and runs `playwright install --only-shell --no-remove chromium`,
  adding `--with-deps` when `CI` is set. The CI e2e job runs `just e2e`
  unchanged, so no `.github/` change is needed.
