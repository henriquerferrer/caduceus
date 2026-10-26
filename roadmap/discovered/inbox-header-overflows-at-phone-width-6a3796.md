---
title: Inbox header overflows at phone width
kind: gap
discovered_from: F1.5
discovered_by: feat/f1-5-verification-badge
milestone_hint: 
created: 2026-10-26T21:28:05.305Z
---

internal/server/ui/index.html: at a 390px viewport the header (title, count, search, Clear all) does not fit, so the Clear all button is clipped at the right edge; the page predates F1.5, which did not change the header. Done: at 390px the header controls fit with no horizontal overflow, checked by the e2e/ui fixture.
