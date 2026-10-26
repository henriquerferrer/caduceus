# Caduceus

Caduceus catches the SMS your application sends in development and tests, the way [Mailpit](https://mailpit.axllent.org) catches email. It is a single binary that speaks your SMS provider's HTTP API, delivers nothing, and shows every message in a web inbox and a REST API.

## Run it

```sh
go run ./cmd/caduceus          # UI, API and provider endpoints on http://localhost:8026
```

Or with Docker:

```sh
docker build -t caduceus . && docker run -p 127.0.0.1:8026:8026 -v caduceus:/data caduceus
```

Send a test message and open http://localhost:8026:

```sh
curl -d '{"to":"+15551234567","body":"Your code is 482913"}' http://localhost:8026/api/v1/send
```

## Point your app at it

Caduceus is also an HTTPS proxy. Requests for a provider it emulates are answered by Caduceus; every other host is tunnelled through to the real server untouched. So an unmodified app needs only two environment variables in development:

```sh
HTTP_PROXY=http://localhost:8026
NODE_EXTRA_CA_CERTS="$HOME/Library/Application Support/caduceus/ca.pem"   # Node
```

The first sends the SDK's traffic through Caduceus. The second makes the app trust the local certificate authority that Caduceus creates on first run, which it uses to answer as `api.twilio.com` and `verify.twilio.com`. The CA path is printed at startup and the certificate is also served at `http://localhost:8026/ca.pem`. Without it the request fails with a certificate error; it never reaches the real provider.

| Runtime | Proxy variable | CA variable |
|---|---|---|
| Node (Twilio SDK) | `HTTP_PROXY` | `NODE_EXTRA_CA_CERTS` (verified; `TWILIO_CA_BUNDLE` is ignored by the SDK when a proxy is set) |
| Python (`requests`) | `HTTPS_PROXY` | `REQUESTS_CA_BUNDLE` (not yet verified) |
| Go, Ruby, curl | `HTTPS_PROXY` | `SSL_CERT_FILE` (not yet verified) |

Emulated so far: Twilio Programmable Messaging (create, fetch and list messages on `api.twilio.com` and its regional hosts) and Twilio Verify (below). Any credentials are accepted for those calls.

Everything else a provider offers keeps working. Caduceus intercepts only the hosts that carry an emulated API, and on those hosts it forwards every endpoint it doesn't emulate to the real provider with your real credentials. Each host answers only its own API: Verify paths sent to `api.twilio.com` and Messaging paths sent to `verify.twilio.com` go to Twilio. With Twilio, voice calls and Lookup reach Twilio as usual.

### Twilio Verify

Verification codes land in the Caduceus inbox instead of on a real phone. On `verify.twilio.com` (and its regional hosts, or the local base URL) Caduceus answers:

| Endpoint | What it does |
|---|---|
| `POST /v2/Services/{VA…}/Verifications` | Starts a verification (`To`, `Channel`; `CustomCode` optional) and captures the code as a message |
| `GET /v2/Services/{VA…}/Verifications/{VE…}` | Fetches it while it is pending |
| `POST /v2/Services/{VA…}/Verifications/{VE…}` | Sets `Status` to `canceled` or `approved` while it is pending |
| `POST /v2/Services/{VA…}/VerificationCheck` | Checks a `Code` against the pending verification named by `VerificationSid` or `To` |

- Any service SID starting with `VA` works with no setup, so your app's existing one does. Fetching, updating or checking a verification through a different service SID answers `20404`.
- The channels `sms`, `call`, `whatsapp`, `email`, `sna` and `auto` are captured; the message's `channel` says which. Phone channels need an E.164 `To` (`+` then digits); `email` needs an address.
- The code is 6 random digits, or your `CustomCode`. The captured message's `otp` is the code, so `/api/v1/wait` returns it for a phone recipient.
- Twilio's limits apply: a verification is pending for 10 minutes, a send to a recipient with a pending verification on the same service resends the same code, and the send after the fifth is refused with error `60203` (HTTP 429).
- Checking: the right code answers `status: approved`, `valid: true`; a wrong one answers `status: pending`, `valid: false` and uses up one of 5 check attempts. The check after the fifth is refused with error `60202` (HTTP 429) and ends the verification. A check with no `Code`, with neither `To` nor `VerificationSid`, or with a `To` that is neither E.164 nor an email address, answers `60200` (HTTP 400).
- Once a verification is approved, canceled, expired or out of check attempts, checking or fetching it answers `20404` (HTTP 404). Twilio documents a 404 for checking an approved, expired or out-of-attempts verification, which it deletes.
- Other start parameters (`Locale`, `CustomMessage`, `CustomFriendlyName`, `TemplateSid`, `AppHash`, `Amount`, `Payee`, `RiskCheck`, `DeviceIp` and the rest) are accepted and ignored.
- Verify captures are not Messaging resources: the Messages API does not list or fetch them.
- Not emulated, so passed through to Twilio: Services, Rate Limits, Messaging Configurations, Webhooks, Access Tokens and TOTP Entities, Factors and Challenges.

Without the proxy, send to Caduceus directly:

| Sender | How |
|---|---|
| Twilio-shaped clients | Use `http://localhost:8026` as the base URL in place of `https://api.twilio.com`. |
| Anything else | `POST /api/v1/send` with JSON `{"to", "from", "body", "status_callback"}`. |

Caduceus listens on `127.0.0.1` by default because an open proxy should not be reachable from the network. The Docker image listens on all interfaces inside the container; publish the port to localhost only (`-p 127.0.0.1:8026:8026`).

## Read messages from tests

```sh
curl -X DELETE localhost:8026/api/v1/messages                 # reset before a test
curl 'localhost:8026/api/v1/wait?to=%2B15551234567&timeout=10s'  # newest message to a number
```

`/api/v1/wait` returns the newest outbound message to the number, and blocks until one arrives if there is none yet. The response includes `otp`, the first 4 to 8 digit code found in the body. It answers `408` if nothing arrives in time.

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/messages?to=&q=&direction=` | List messages, newest first |
| `GET /api/v1/messages/{id}` | One message |
| `DELETE /api/v1/messages` | Clear the inbox |
| `GET /api/v1/wait?to=&timeout=&since=` | Wait for a message to a number |
| `POST /api/v1/messages/{id}/status` | Force a status such as `failed`; fires the status callback |
| `POST /api/v1/inbound` | Simulate a reply: `{"from", "to", "body", "webhook"}` posts a Twilio-shaped webhook to your app |
| `GET /api/v1/events` | Server-sent events for every change |

## Delivery simulation

Each captured message moves from `queued` to `sent` to `delivered`, and Caduceus posts Twilio-style status callbacks to the message's `StatusCallback` URL. Callbacks are not signed yet, so disable `X-Twilio-Signature` validation in development.

## Options

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `-listen` | `CADUCEUS_LISTEN` | `127.0.0.1:8026` | Listen address |
| `-data-dir` | `CADUCEUS_DATA_DIR` | user config dir | Where the proxy CA certificate and key are kept |
| `-database` | `CADUCEUS_DATABASE` | temporary | SQLite file for messages |
| `-max-messages` | | `5000` | Messages kept before the oldest are dropped (0 keeps all); also caps Verify verifications, counted separately |
| `-delivery-delay` | | `1s` | Pause before each simulated status change; negative disables |
| `-inbound-webhook` | `CADUCEUS_INBOUND_WEBHOOK` | | Default URL for simulated inbound messages |

## Storage

Messages are stored in SQLite. By default Caduceus uses a temporary database that is deleted when it exits, so every run starts with an empty inbox. Pass `-database caduceus.db` to keep messages across restarts.
