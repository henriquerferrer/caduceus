# ROADMAP (index)

Direction-level view only. **Backlog items live one file each under
`roadmap/`** (`roadmap/<ID>.md`, e.g. `roadmap/F1.1.md`) — see the sweap
playbook's **Backlog format** section for the item schema (frontmatter:
`id`, `title`, `status`, `depends_on`, `review-dimensions`, optional `followup_of` /
`harm` / `premise` / `blocked` / `handoff`; body: `## Spec` +
`## Acceptance` — or `## Hand-off` for a `status: attended` item, a
planned human hand-off that is never dispatched to an agent). Status truth is
the item files; this index never carries per-item status. Dropped items move to
`roadmap/archive/`.

## Milestones (priority order)

- F1 — Twilio Verify emulation: an unmodified app's Verify start/check is answered by Caduceus on `verify.twilio.com`, and every channel's code lands in the inbox (items `roadmap/F1.1.md`–`F1.6.md`).
  Success signal: an app's unchanged Verify sign-up test passes against Caduceus, reading the code from `/api/v1/wait`, and repeated wrong codes reach Twilio's real max-attempts error.

## Later milestones (sketch, not yet specced)

- F2 — More providers: AWS SNS via `AWS_ENDPOINT_URL_SNS`, Vonage SMS
- F3 — Test helpers: Playwright/Cypress fixtures around `/api/v1/wait`
- F4 — Fidelity: signed status callbacks (`X-Twilio-Signature`), proxy mode verified from Python, Go, Ruby and curl
- F5 — Distribution: release binaries and container image, landing page
