# F1.6 — Stock twilio Node SDK drives Verify through the proxy (design)

Item: `roadmap/F1.6.md` (the item file is the authoritative spec; this note
pins the decisions it leaves to the implementer).

## Script (`e2e/twilio-node/verify.mjs`)

- `node verify.mjs start TO` calls
  `client.verify.v2.services(SID).verifications.create({to, channel: "sms"})`;
  `node verify.mjs check TO CODE` calls
  `client.verify.v2.services(SID).verificationChecks.create({to, code})`.
  The service SID (`VAe2e…`) and the credentials are made up, as in
  `send.mjs`.
- On success it prints `{"sid", "status", "valid", "dateCreated"}`, with
  `dateCreated` as the SDK's parsed `Date` rendered by `toISOString()`, so
  the date went through the SDK's ISO 8601 deserializer.
- On a `twilio.RestException` it prints
  `{"restException": {"status", "code"}}` and exits 0, so the Go test can
  assert the error the SDK parsed. Any other error is rethrown and exits
  non-zero, and the Go test fails with the script's output.
- No dependency change: it uses the `twilio` package `send.mjs` already
  pins.

## Test (`e2e/e2e_test.go`, `TestTwilioNodeSDKVerify`)

One test drives one recipient through the lifecycle with the script, with
only `HTTP_PROXY`, `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS` set:
start (a `VE` sid, `pending`, not valid, `dateCreated` within a minute of
now) → `/api/v1/wait` (the message's `otp` and `verification_sid`) → a
second start (same sid) → a check with a wrong code (`pending`, not valid)
→ a check with the code (`approved`, valid, same sid) → a check after
approval (`RestException` 404 / 20404).

The fixture guard is the one `TestTwilioNodeSDK` uses: with `CI` set and no
`e2e/twilio-node/node_modules/twilio`, the test fails; without `CI` it
skips. `TestTwilioNodeSDK` itself is unchanged.
