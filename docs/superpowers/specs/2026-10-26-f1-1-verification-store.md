# F1.1 — Verification store and lifecycle rules (design)

Item: `roadmap/F1.1.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## Data model

- `messages` gains `channel` and `verification_sid` (`TEXT NOT NULL DEFAULT ''`).
- New table `verifications` in the same database: `seq` (autoincrement, the
  newest-first and pruning order), `id` (unique), `service_sid`,
  `account_sid`, `recipient`, `recipient_digits`, `channel`, `code`,
  `status`, `send_attempts` (JSON array of `{sid, channel, time}`),
  `check_attempts`, `created_at`, `updated_at`, `expires_at` (Unix
  nanoseconds, like messages).
- Recipient normalisation, shared by messages and verifications: a recipient
  containing `@` is stored lowercased with `recipient_digits = ''`; any other
  recipient is stored as given with its digits.

## Store methods

- `AddVerification` sets `CreatedAt` and `UpdatedAt` to now and `ExpiresAt`
  to `CreatedAt + VerificationTTL`; it ignores any `ExpiresAt` the caller
  passes. Tests that need an expired verification move `ExpiresAt` with
  `UpdateVerification`.
- `UpdateVerification` writes the mutable fields (`Channel`, `Code`,
  `Status`, `SendAttempts`, `CheckAttempts`, `ExpiresAt`), sets `UpdatedAt`
  to now, and returns the row as stored (expiry is applied on read, not on
  this write). An unknown id is `ErrNotFound`.
- `GetVerification`, `ListVerifications` and `PendingVerification` apply
  expiry on read, using the conditional `UPDATE` from the item. Rows are read
  and closed before any expiry write (the store runs on one connection).
- `PendingVerification` with a recipient that has neither `@` nor digits
  returns `ErrNotFound` rather than matching every email recipient.
- Every verification write publishes `Event{Type: "verification"}` carrying
  the verification as stored.

## Migration

`Open` runs the schema (which creates the new tables and columns on a fresh
database), then, when `messages` lacks the `channel` column, in one
transaction: adds `channel` and `verification_sid`, and rewrites every
recipient containing `@` to lowercase (Go's `strings.ToLower`, the same
function new writes use) with empty digits. A migrated database has the
column, so reopening it skips the step.

## Layers

Every acceptance clause is asserted by a store unit test in
`internal/store/store_test.go`, except the README clause.
