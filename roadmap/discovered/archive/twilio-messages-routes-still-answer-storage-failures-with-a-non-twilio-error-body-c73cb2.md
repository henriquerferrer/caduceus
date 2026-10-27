---
title: Twilio Messages routes still answer storage failures with a non-Twilio error body
kind: gap
discovered_from: F1.2
discovered_by: feat/f1-2-verify-start
milestone_hint: 
created: 2026-10-26T20:41:41.145Z
declined: parked: no harm class — only a storage failure reaches it, not reachable by user steps
---

internal/server/twilio.go:127: The Twilio Messages routes (create, fetch and list, at lines 127, 142 and 151) still answer storage failures through s.fail with Caduceus own {"error":"storage error"} body instead of Twilio 20500 error shape (code, message, more_info, status). The Verify routes now use the Twilio shape, so a stock SDK gets a non-Twilio body on a Messages 500. This code predates this diff.
