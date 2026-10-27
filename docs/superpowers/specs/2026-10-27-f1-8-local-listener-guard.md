# F1.8 — Refuse cross-site and unknown-Host requests on the local listener (design)

Item: `roadmap/F1.8.md` (the item file is the authoritative spec; answer (b)
was taken). This note pins the decisions it leaves to the implementer.

## Where the guard sits

`Server.ServeHTTP` has three branches: CONNECT, absolute-form proxy requests
(`serveProvider`) and the `default:` branch that hands off to `s.mux`. The
guard runs in the `default:` branch only, before `s.mux`. Tunnelled requests
are served by a separate `http.Serve` on `connListener` with
`serveTunnelled` as the handler, so they never pass through `ServeHTTP` and
need no carve-out.

## Host rule (`guard.go`)

- `hostName(h)`: strip the port (`net.SplitHostPort`, falling back to the raw
  value when there is none), strip IPv6 brackets, lower-case, drop one
  trailing dot.
- Built-in names: `localhost`, `127.0.0.1`, `::1`. `Options.AllowedHosts`
  entries are normalised with the same function when the server is built;
  empty entries are ignored.
- An empty `Host` normalises to `""`, which is never in the list.
- `SplitHosts(s)` splits the flag/env value on commas and trims spaces; the
  command passes its result as `Options.AllowedHosts`.

## Cross-site rule

Refused when `Sec-Fetch-Site` is `cross-site` or `same-site`, or when an
`Origin` header is present and is not `http://` + the request's `Host`
(compared case-insensitively; `null` never matches). Exempt: a `GET` or
`HEAD` with `Sec-Fetch-Mode: navigate`, unless `Sec-Fetch-Dest` is present
and is not `document` — the item says *top-level* navigation, and a framed
navigation (`iframe`, `frame`, `embed`, `object`) is not one.

## Refusal

403. `/2010-04-01/` and `/v2/` paths answer `twilioError(…, 20403, msg)`;
every other path answers `writeError`. Logged once with `slog` at info level:
host, origin, method, path. The Host message names `-allowed-hosts` and
`CADUCEUS_ALLOWED_HOSTS`.

## Command and docs

`-allowed-hosts` flag, defaulting to `CADUCEUS_ALLOWED_HOSTS`. README:
an Options row and a Docker Compose example.
