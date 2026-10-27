---
title: GET /api/v1/wait cannot wait for an email recipient
kind: gap
discovered_from: F1.1
discovered_by: feat/f1-1-verification-store
milestone_hint: 
created: 2026-10-26T20:15:09.942Z
declined: parked: premise invalidated — handleWait now accepts an email address (internal/server/server.go:409), no harm on main
---

/Users/henrique/Documents/caduceus-worktrees/f1-1/internal/server/server.go:298: handleWait still returns 400 "to" must be a phone number when Digits(to) is empty. Now that the store keeps email recipients and Message gains Channel (an email-channel Verify message will be stored with To=email), GET /api/v1/wait cannot wait for an email recipient. This diff does not touch that code, and this item spec leaves the server out of scope. A later F1 item should decide whether wait accepts an address.
