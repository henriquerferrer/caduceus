---
title: TestVerifyWriteKeepsExpiryStoredAfterItsRead fails locally on go1.27.3 darwin/arm64
kind: gap
discovered_from: F1.8
discovered_by: fix/f1-8-local-listener-guard
milestone_hint: 
created: 2026-10-27T23:26:45.818Z
declined: fixed directly on branch fix/verify-expiry-test-without-race — the test left delivery simulation on, whose goroutine took the pausing clock outside verifyMu at full speed, and it now runs with DeliveryDelay -1, and just test adds a full-speed shuffled pass so a test that only holds under -race fails in CI
---

TestVerifyWriteKeepsExpiryStoredAfterItsRead fails locally on go1.27.3 darwin/arm64 with: panic: Fail in goroutine after TestVerifyWriteKeepsExpiryStoredAfterItsRead/resend_on_start has completed (server_test.go:1398, goroutine started at :1384). Reproduced in the worktree. server_test.go is not in this branch diff. Because of this failure, just ci-local is red locally whatever this item changes. The fixer has already filed it as a gap note.
