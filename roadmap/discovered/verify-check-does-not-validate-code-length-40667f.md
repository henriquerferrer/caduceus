---
title: Verify check does not validate Code length
kind: direction
discovered_from: F1.3
discovered_by: feat/f1-3-verify-check
milestone_hint: 
created: 2026-10-26T20:53:42.620Z
---

internal/server/verify.go verifyCheck accepts any non-empty Code. Twilio's 60200 page (https://www.twilio.com/docs/api/errors/60200) lists, for checking a verification, "Code parameter is not between 4-10 numbers long" as a cause of 60200 (HTTP 400). Caduceus counts such a check as a wrong attempt instead. Done: decide whether an out-of-range Code returns 60200 like Twilio, together with the CustomCode length note from F1.2.
