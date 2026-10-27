---
title: Inbox load() has no request sequencing for the messages list
kind: gap
discovered_from: F1.5
discovered_by: feat/f1-5-verification-badge
milestone_hint: 
created: 2026-10-26T21:38:58.136Z
declined: parked: no harm class — a stale list is transient and the next event or search reloads it; same reasoning as F1 audit block 3 (parked in F1.9)
---

internal/server/ui/index.html: load() has no request sequencing: overlapping loads (search keystrokes, event bursts) can resolve out of order and render an older messages list over a newer one. The diff verification map is guarded by newer(), the messages list is not.
