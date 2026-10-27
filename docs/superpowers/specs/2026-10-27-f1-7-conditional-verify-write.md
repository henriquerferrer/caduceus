# F1.7 — A Verify write never overwrites a stored expiry (design)

Item: `roadmap/F1.7.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## The race

The Verify handlers hold `verifyMu`, read a verification (expiry applied),
and write the whole row back. `GET /api/v1/verifications` and
`GET /api/v1/verifications/{sid}` apply and store expiry without taking
`verifyMu`. If the TTL ends between a handler's read and its write and one
of those reads runs in that window, it stores and publishes `expired`; the
handler's write then replaced it with the status the handler computed from
its stale read.

## Store (`internal/store`)

- `ErrStale` — a conditional write found the verification, but its stored
  status is no longer the one the caller read.
- `UpdateVerification(v Verification, from string)` — writes as before, with
  `WHERE id = ? AND status = ?`, `from` being the status the caller read.
  No row matched: `ErrNotFound` when the row is gone, else `ErrStale`.
  Nothing is written or published in either case.
- `SendVerification(v, isNew, m)` — a resend does not change the status, so
  its update is conditional on the stored status still being `v.Status`.
  A stale resend rolls back: no message is stored and nothing is published.
- `(*Store).SetClock(func() time.Time)` — replaces the store's clock, so
  server tests inject time without reflection. It is not synchronised: it
  is called before the store is shared.

## Server (`internal/server`)

Each Verify write path runs its read and its write in a loop under
`verifyMu`. A write that returns `ErrStale` goes round again from a fresh
read, so the answer comes from the current state:

- check (`verifyCheck`, including the `max_attempts_reached` write): the
  re-read finds no pending verification, so 404 code 20404.
- update (`verifyUpdate`): the re-read is not pending, so 404 code 20404.
- start (`verifyStart`): the re-read finds no pending verification for the
  service and recipient, so it creates a new one and sends its code (201).
- the expire hook (`POST /api/v1/verifications/{sid}/expire`): the re-read is
  not pending, so it answers 409 as it does for any verification that is not
  pending.

The loop goes round again on `ErrStale` alone, which needs a write outside
`verifyMu` to change the row after this request read it. The one such writer
today is the expiry a read applies, and it moves a row from `pending` to
`expired`, which no read moves back.

`handleListVerifications` and `handleGetVerification` keep not taking
`verifyMu`: the conditional write is what closes the race, and the
acceptance test needs those reads to run while a handler is paused.

## Tests

- Store: an update conditioned on `pending` against a stored `expired` row
  returns `ErrStale`, leaves the row unchanged and publishes nothing; a
  stale resend through `SendVerification` stores no message.
- Server: a clock that pauses the first call after it is armed, returning
  the time it read before pausing. Each handler's first clock call is the
  expiry check of its read, so the pause falls after the read and before
  the write, outside any transaction (the store has one connection). In
  the window a `GET /api/v1/verifications/{sid}` with the clock past the
  TTL stores `expired`; after release the verification is still `expired`
  and its events end at `expired`. Cases: check with the right code, check
  with a wrong code, the check that reaches the attempt limit, update to
  `approved`, resend on start, and the expire hook.
