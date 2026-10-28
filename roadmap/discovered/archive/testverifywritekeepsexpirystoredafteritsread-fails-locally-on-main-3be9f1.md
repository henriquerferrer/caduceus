---
title: TestVerifyWriteKeepsExpiryStoredAfterItsRead fails locally on main
kind: gap
discovered_from: F1.7
discovered_by: fix/f1-8-local-listener-guard
milestone_hint: 
created: 2026-10-27T23:23:24.344Z
declined: fixed directly on branch fix/verify-expiry-test-without-race: the test left delivery simulation on, whose goroutine took the pausing clock outside verifyMu at full speed; it now runs with DeliveryDelay -1, and just test adds a full-speed shuffled pass so a test that only holds under -race fails in CI
---

On origin/main 33a230e with go1.27.3 darwin/arm64, go test -count=1 -run TestVerifyWriteKeepsExpiryStoredAfterItsRead ./internal/server/ fails every run at server_test.go:1418 'handler paused at its read without holding verifyMu' (subtests check right code, check wrong code, and others). Reproduced in a clean detached worktree of origin/main, three runs out of three. Done looks like: the test passes reliably locally and in CI, or the reason it passes in CI but not here is found and fixed.
