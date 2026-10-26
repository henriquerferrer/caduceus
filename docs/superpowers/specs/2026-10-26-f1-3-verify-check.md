# F1.3 — Emulate checking a verification (design)

Item: `roadmap/F1.3.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## Route

`POST /v2/Services/{service}/VerificationCheck`, registered by
`verifyRoutes`, so it is on the local mux and on the `verify.*` provider mux.
The `api.*` mux does not carry it, so a check sent to `api.twilio.com` passes
through to Twilio.

## Handler (`internal/server/verify.go`, `verifyCheck`)

Order of checks:

1. A service segment not starting with `VA` is `20404`, as on start.
2. Form parsing failure is `20001`.
3. Neither `To` nor `VerificationSid`: `60200`, `Invalid parameter: To`.
   Missing `Code`: `60200`, `Invalid parameter: Code`. Both HTTP 400.
4. Under `verifyMu`: find the verification. With `VerificationSid` (it wins
   when both are sent) the row is read by sid, expiry applied, and a row on
   another service counts as not found. With `To` only, the newest live
   pending verification on the path's service (`PendingVerification`).
5. Not found, or not `pending` (approved, canceled, expired,
   max_attempts_reached): `20404`, HTTP 404, no attempt counted.
6. `CheckAttempts` already at `MaxCheckAttempts` (5): the status is stored
   as `max_attempts_reached` and the answer is `60202`, HTTP 429. That
   check is not counted, so the stored attempts never exceed the limit.
   Per Twilio's 60202 page the limit is exceeded when checking "more than 5
   times", so the sixth check gets 60202 whatever its code.
7. Otherwise one check attempt is counted. A code equal to the stored code
   (constant-time comparison) sets `approved`. The answer is 200 with the
   VerificationCheck resource.

## Resource

`sid` (the `VE…` sid), `service_sid`, `account_sid`, `to`, `channel`,
`status`, `valid` (true only when approved), `amount` and `payee` (null),
`date_created`, `date_updated` (ISO 8601 UTC, as F1.2 renders them) and
`sna_attempts_error_codes` (an empty list).

## Fetch

`GET …/Verifications/{sid}` now answers `20404` for a verification that is
not pending, as an update of one already did.
