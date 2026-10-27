---
title: Inbox messages read does not check res.ok
kind: gap
discovered_from: F1.5
discovered_by: feat/f1-5-verification-badge
milestone_hint: 
created: 2026-10-26T21:38:58.217Z
declined: parked: no harm class — /api/v1/messages fails only on a storage error (the search is an escaped LIKE), not reachable by user steps
---

internal/server/ui/index.html:93: messages = (await res.json()).messages does not check res.ok, while the verifications read next to it is guarded. A failed messages read sets messages to undefined and render() throws on the for-of loop.
