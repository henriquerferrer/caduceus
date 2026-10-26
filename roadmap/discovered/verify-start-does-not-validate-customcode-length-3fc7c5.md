---
title: Verify start does not validate CustomCode length
kind: direction
discovered_from: F1.2
discovered_by: feat/f1-2-verify-start
milestone_hint: 
created: 2026-10-26T20:26:24.304Z
---

internal/server/verify.go verifyStart accepts any non-empty CustomCode. Twilio's Verification docs (https://www.twilio.com/docs/verify/api/verification) say the code can be between 4 and 10 characters, inclusive, so real Twilio would reject e.g. CustomCode=123 while Caduceus captures it. Done: decide whether an out-of-range CustomCode returns 60200 (HTTP 400) like Twilio.
