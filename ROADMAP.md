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

- F1 — Twilio Verify emulation: an unmodified app's Verify start/check is answered by Caduceus on `verify.twilio.com`, and every channel's code lands in the inbox (items `roadmap/F1.1.md`–`F1.6.md`; audited in `roadmap/audits/F1.md`, follow-ups F1.7–F1.12).
  Success signal: an app's unchanged Verify sign-up test passes against Caduceus, reading the code from `/api/v1/wait`, and repeated wrong codes reach Twilio's real max-attempts error.
- F2 — More providers: AWS SNS `Publish` and End User Messaging `SendTextMessage` through their per-service endpoint env vars, and in proxy mode on the AWS hosts; the Vonage SMS API and Messages API (sms channel) through the proxy, with delivery receipts and Vonage-shaped simulated inbound; responses checked against real captures (items `roadmap/F2.1.md`–`F2.10.md`; F2.9 is a hand-off).
  Success signal: an unmodified app's SMS sign-up test passes with each of the four SDK calls (SNS Publish, SendTextMessage, Vonage `sms.send`, Vonage `messages.send`), reading the code from `/api/v1/wait`, while its non-SMS SNS calls still reach AWS in proxy mode.
  Not in F2: the Vonage Verify API; AWS inbound / two-way SMS; checking receipt and inbound webhook shapes against real captures; a real-SDK e2e of AWS proxy mode (moved to F4).

## Later milestones (sketch, not yet specced)

- F3 — Test helpers: Playwright/Cypress fixtures around `/api/v1/wait`
- F4 — Fidelity: signed status callbacks (`X-Twilio-Signature`, Vonage `sig` and Messages JWT), proxy mode verified from Python, Go, Ruby and curl (including AWS SNS and End User Messaging through the proxy with boto3 and Go, deferred from F2.4)
- F5 — Distribution: release binaries and container image, landing page
