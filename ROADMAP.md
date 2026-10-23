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

- F1 — Twilio Verify emulation: start/check verification in-process, codes land in the inbox (not yet specced — run `/roadmap`)

## Later milestones (sketch, not yet specced)

- F2 — More providers: AWS SNS via `AWS_ENDPOINT_URL_SNS`, Vonage SMS
- F3 — Test helpers: Playwright/Cypress fixtures around `/api/v1/wait`
- F4 — Fidelity: signed status callbacks (`X-Twilio-Signature`), proxy mode verified from Python, Go, Ruby and curl
- F5 — Distribution: release binaries and container image, landing page
