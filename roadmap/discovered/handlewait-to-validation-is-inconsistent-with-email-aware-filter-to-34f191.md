---
title: handleWait to-validation is inconsistent with email-aware Filter.To
kind: gap
discovered_from: F1.1
discovered_by: feat/f1-1-verification-store
milestone_hint: 
created: 2026-10-26T20:15:06.046Z
---

/Users/henrique/Documents/caduceus-worktrees/f1-1/internal/server/server.go:298: handleWait validates `to` with `store.Digits(f.To) == ""` ("to" must be a phone number). An email with no digits (ada@example.com) is rejected, but one with a digit (ada1@example.com) is accepted and, after this diff, matched as an exact email address. That validation is now inconsistent with the store email-aware Filter.To. Not part of F1.1 scope (no HTTP routes), but a later F1 item that waits on email channels will need to change it.
