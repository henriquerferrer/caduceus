---
title: Plain-HTTP proxy requests to provider hosts skip the Origin rule
kind: gap
discovered_from: F1.8
discovered_by: fix/f1-8-local-listener-guard
milestone_hint: 
created: 2026-10-27T23:26:45.690Z
---

Plain-HTTP proxy requests (the ServeHTTP case for absolute URLs, which calls serveProvider) to http://verify.twilio.com/v2/... or http://api.twilio.com/2010-04-01/... skip the Origin rule by the item own decision. A browser that happened to use Caduceus as its HTTP proxy could therefore be made by another site to submit forged POSTs to the emulated provider routes. The impact is small because an attacker would need a VE sid or the code. This is a residual risk, not a defect in this diff. See internal/server/server.go.
