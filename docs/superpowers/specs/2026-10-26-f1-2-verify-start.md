# F1.2 — Intercept verify.twilio.com and emulate starting a verification (design)

Item: `roadmap/F1.2.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## Routing

- `intercepted(host)`: `api.*.twilio.com` or `verify.*.twilio.com`.
- The server keeps two provider muxes, `messaging` (Messages routes) and
  `verify` (Verify routes). `providerRoutes(host)` picks `verify` for a
  `verify.*` host, `messaging` for any other intercepted host, and none for
  a host that is not intercepted. The local mux carries both route sets, for
  base-URL clients.
- `serveProvider(w, r, host, pass)` answers a request that matches a route
  of its host's mux, and hands anything else to `pass`. HTTPS-tunnelled
  requests pass to the reverse proxy (`upstream`); plain-HTTP absolute-form
  requests pass to `forward` (`direct`). An absolute-form request is never
  served by the local mux, so it never reaches the Caduceus API or UI.

## Verify handlers (`internal/server/verify.go`)

- Every handler holds `verifyMu` across its reads and writes (fetch too,
  because a read can store an expiry).
- Start: a service segment not starting with `VA` is `20404`; then
  validation (`60200`, message `Invalid parameter: To|Channel`); then
  `PendingVerification(service, to)`. A live pending one is reused: refused
  with `60203` (HTTP 429) when it already has `MaxSendAttempts` attempts,
  otherwise its channel is updated and one `VL…` attempt appended. Otherwise
  a new `VE…` verification is added with the `CustomCode` or a random
  6-digit code. Each accepted send captures one message (provider
  `twilio-verify`, `From: Verify`, OTP preset to the code) and answers 201.
- Fetch / update look the verification up by sid and treat a different
  service segment as not found (`20404`). Update accepts `Status` of
  `canceled` or `approved` (anything else is `60200`) and only on a pending
  verification (otherwise `20404`).
- Rendering: Twilio's Verification fields, dates as
  `2006-01-02T15:04:05Z`, `lookup` as `{}`, `amount`/`payee` null, `valid`
  true only when approved, `url` built from the request's scheme and host,
  `sna` a `{"url": "http://<local address>/sna/<sid>"}` object on the `sna`
  channel only.
- `account_sid`: Basic-auth user when it starts with `AC`, else
  `AC00000000000000000000000000000000`.

## Messaging changes

- `twilioError(w, status, code, msg)`; `twilioNotFound` writes `20404`.
- `capture` keeps a preset `OTP` and derives one from the body otherwise.
- Messages list skips and fetch 404s a message whose provider is
  `twilio-verify`.
