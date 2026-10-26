# F1.4 — Verifications in the Caduceus REST API (design)

Item: `roadmap/F1.4.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## Routes (`internal/server/server.go`, local mux only)

- `GET /api/v1/verifications?to=` — `{"verifications": [...]}` from
  `store.ListVerifications(Filter{To})`, newest first, expiry applied. The
  body is the store's `Verification` JSON. A `to` with neither digits nor
  `@` does not filter, as on `GET /api/v1/messages`.
- `GET /api/v1/verifications/{sid}` — `store.GetVerification`, expiry
  applied. Absent: 404 `{"error": "verification not found"}`.
- `POST /api/v1/verifications/{sid}/expire` — under `verifyMu`: read the
  verification (expiry applied); absent is 404; a status other than
  `pending` is 409 `{"error": "verification is <status>, not pending"}`.
  Otherwise `ExpiresAt` is set to now with `UpdateVerification` and the
  verification is read back, which stores it as `expired`; the answer is 200
  with that verification.

The routes are not on the `api.*` or `verify.*` provider muxes, so proxied
requests never reach them.

## Reset

`DELETE /api/v1/messages` already deleted verifications (`store.Clear`) and
already held `verifyMu` before this item; a test now pins that the
verifications list is empty afterwards.

## Wait

`GET /api/v1/wait` accepts a `to` that contains `@`, matched by the store's
email-aware `Filter` (case-insensitive). A `to` with neither digits nor `@`
is still 400.
