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

The first sends the SDK's traffic through Caduceus. The second makes the app trust the local certificate authority that Caduceus creates on first run, which it uses to answer as `api.twilio.com`. The CA path is printed at startup and the certificate is also served at `http://localhost:8026/ca.pem`. Without it the request fails with a certificate error; it never reaches the real provider.

| Runtime | Proxy variable | CA variable |
|---|---|---|
| Node (Twilio SDK) | `HTTP_PROXY` | `NODE_EXTRA_CA_CERTS` (verified; `TWILIO_CA_BUNDLE` is ignored by the SDK when a proxy is set) |
| Python (`requests`) | `HTTPS_PROXY` | `REQUESTS_CA_BUNDLE` (not yet verified) |
| Go, Ruby, curl | `HTTPS_PROXY` | `SSL_CERT_FILE` (not yet verified) |

Emulated so far: Twilio Programmable Messaging (create, fetch and list messages on `api.twilio.com` and its regional hosts). Any credentials are accepted for those calls.

Everything else a provider offers keeps working. Caduceus intercepts only the host that carries an emulated API, and on that host it forwards every endpoint it doesn't emulate to the real provider with your real credentials. With Twilio, voice calls, Verify and Lookup all reach Twilio as usual. Note that Twilio Verify sends its own SMS from Twilio's side, so those codes go to real phones and do not appear in Caduceus.

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
| `-max-messages` | | `5000` | Messages kept before the oldest are dropped (0 keeps all) |
| `-delivery-delay` | | `1s` | Pause before each simulated status change; negative disables |
| `-inbound-webhook` | `CADUCEUS_INBOUND_WEBHOOK` | | Default URL for simulated inbound messages |

## Storage

Messages are stored in SQLite. By default Caduceus uses a temporary database that is deleted when it exits, so every run starts with an empty inbox. Pass `-database caduceus.db` to keep messages across restarts.
