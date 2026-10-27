---
title: Origin check refuses an HTTPS reverse proxy in front of Caduceus
kind: direction
discovered_from: F1.8
discovered_by: fix/f1-8-local-listener-guard
milestone_hint: 
created: 2026-10-27T23:23:24.303Z
---

The local-listener guard (internal/server/guard.go, refusal) accepts an Origin only when it equals http:// + Host, as the F1.8 spec defines. Behind an HTTPS reverse proxy whose name is in -allowed-hosts, the browser sends Origin https://<host>, so the inbox page loads but its writes (clear, expire, simulate inbound) answer 403. Done looks like: a decision on whether that setup is supported; if it is, the check accepts the matching https origin (or a configured origin) and the README says so; if not, the README says Caduceus is reached over plain http.
