---
title: Local REST API has no Origin or Host check
kind: gap
discovered_from: F1.4
discovered_by: feat/f1-4-verifications-rest-api
milestone_hint: 
created: 2026-10-26T21:15:46.805Z
---

internal/server/server.go: The local REST API has no Origin or Host check. A page open in the developer browser can send a CORS-simple cross-site POST to the new /api/v1/verifications/{sid}/expire route, which needs no body, as it already can to the existing POST routes. A DNS-rebinding page could also read captured codes. The new route joins a class of exposure that already existed.
