---
title: Verify handlers stamp times with the wall clock, not the store clock
kind: direction
discovered_from: F1.7
discovered_by: fix/f1-7-conditional-verify-write
milestone_hint: 
created: 2026-10-27T22:59:59.844Z
---

handleExpireVerification sets ExpiresAt with time.Now() and verifyStart stamps each send attempt's Time with time.Now(), while the store decides expiry and stamps created/updated with its own clock (now settable via Store.SetClock). With an injected store clock the expire hook's ExpiresAt and the attempt times are wall-clock values, so a server test cannot pin them. Done: the server takes those stamps from the store's clock, so one injected clock governs every Verify time.
