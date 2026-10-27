---
title: Inbox render() drops keyboard focus on conversation buttons at every SSE event
kind: gap
discovered_from: F1.5
discovered_by: feat/f1-5-verification-badge
milestone_hint: 
created: 2026-10-26T21:38:58.179Z
declined: parked: no harm class — keyboard focus is lost on new events but every task stays completable; an a11y polish for /roadmap
---

internal/server/ui/index.html: render() replaces the #peers nav buttons on every SSE event, so keyboard focus on a conversation button is lost whenever any message arrives.
