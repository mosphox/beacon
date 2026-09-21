# beacon

A self-hosted "what's my IP" service. One endpoint returns the caller's public
IP address enriched with GeoIP data (city, country, ASN). The same URL serves a
styled web page to browsers and machine-readable output to everything else,
chosen automatically from the request's `User-Agent` and `Accept` headers.

![Beacon](assets/preview.png)

## Features

- **One URL, three audiences.** Browsers get a Next.js page; `curl` gets plain
  text; clients sending `Accept: application/json` get JSON. No separate API
  path.
- **IP enrichment.** City, region, postal code, coordinates, accuracy radius,
  timezone and local time, continent, EU membership, anycast/proxy/satellite
  flags, and ASN (number + organization) — plus the caller's reverse-DNS
  hostname.
- **Multiple GeoIP sources, compared.** DB-IP Lite (no account needed) and
  MaxMind GeoLite2 (optional) answer independently. Where they agree the
  output is unchanged; where they disagree every answer is shown and
  attributed. JSON always carries all of them.
- **Versioned JSON.** The nested `version: 2` object is the default; the
  original flat shape is still available as `?v=1`.
- **Own TLS, optionally.** beacon can terminate TLS itself, obtaining and
  renewing certificates in-process over the ACME DNS-01 challenge, with no
  cron job and no second process.
- **Arbitrary lookups.** `GET /` returns the caller's IP; `GET /{ip}` looks up
  any address.
- **Self-maintaining GeoIP data.** Databases are downloaded on first start and
  refreshed on a schedule, written to a temp file and installed atomically.
  MaxMind archives are additionally SHA-256–verified against the published
  checksum; DB-IP publishes none. No database files live in the repo.
- **Zero-downtime refresh.** Database readers are hot-swapped under a lock; the
  service keeps answering during an update.
- **Hardened.** Non-root containers, static CGO-free Go binary, license key
  redacted from logs, archive size/member caps, graceful shutdown.
- **Plain HTTP by default.** Out of the box the stack speaks plain HTTP: expose
  it directly or front it with your own TLS-terminating proxy. Setting
  `TLS_ENABLED=true` makes beacon terminate TLS itself instead.

## Architecture

Three containers, defined in `docker-compose.yaml`:

| Container            | Image         | Role                                                              |
| -------------------- | ------------- | ----------------------------------------------------------------- |
| `beacon-backend`     | Go 1.25       | Entry point. IP detection, GeoIP lookups, routing, refresh loop.  |
| `beacon-frontend`    | Next.js 14    | The web page (App Router, standalone output). Internal `3000`.    |
| `beacon-init-volume` | `alpine:3.21` | One-shot: fixes ownership/permissions on the data volume.         |

Only the backend is published to the host: `${HOST}:${PORT}` maps to the
container's port 8000, and — when TLS is enabled — `${TLS_PUBLISH}` maps to
8443. The frontend is only reachable inside the compose network; the GeoIP
databases live on a named volume (`beacon-data`).

### Request routing

The backend decides who gets what, in this order:

Checked in this order — the first match wins:

| # | Request                                    | Goes to     |
| - | ------------------------------------------ | ----------- |
| 1 | `/_next/*`, `/favicon.ico`, `/logo.png`    | frontend    |
| 2 | `/healthz`                                 | health JSON |
| 3 | A browser navigation                       | frontend    |
| 4 | `?format=json` / `?format=text`            | that format |
| 5 | `Accept` contains `application/json`       | JSON        |
| 6 | Anything else (curl, wget, scripts, bots)  | plain text  |

Note rows 1 and 3 outrank the format controls: the app's own assets are always
served, and a browser navigation gets the page even when its `Accept` header
also lists JSON. `?format=` does suppress the page (row 3 defers to it), so
`?format=json` in a browser address bar returns raw JSON.

"A browser navigation" is decided by Fetch Metadata first. `Sec-Fetch-*` are
forbidden header names, so page JavaScript cannot forge them, and the browser
labels its own cases for us: a typed URL or followed link is
`Sec-Fetch-Dest: document`, while the page's own `fetch()` for JSON is
`Sec-Fetch-Dest: empty`. That is exactly the distinction needed here.

Browsers that predate Fetch Metadata (Safari before 16.4) fall back to
`User-Agent` plus an explicit appetite for HTML. Known bots, crawlers, link
unfurlers and HTTP libraries are excluded first, because many of them
advertise a browser-shaped `User-Agent`; they get the data, which is what
they actually want.

The page is itself a backend client: it fetches its own pathname with
`Accept: application/json` and renders the result. A browser hitting `/8.8.8.8`
loads the page, which then calls the backend for that IP's data.

### Client IP

With `TRUST_PROXY_HEADERS=true` (the default) the backend resolves the caller's
address in this order:

1. The **last** entry of `X-Forwarded-For`. Proxies append the peer they saw,
   so the last hop is the address the nearest proxy actually observed. Earlier
   entries may have been supplied by the caller and are ignored.
2. `X-Real-IP`, if it parses as an address. Only a fallback: it is a single
   value that proxies *set* rather than append, and one that does not set it —
   Caddy's `reverse_proxy` does not — passes the client's own value straight
   through.
3. The connection peer.

Preferring the appended chain is what makes this safe: a caller sending
`X-Forwarded-For: 1.2.3.4` gets `1.2.3.4, <their real address>` appended by the
proxy and the real address still wins, and a caller sending `X-Real-IP` cannot
override that chain.

This works out of the box behind Caddy or nginx, neither of which needs extra
header configuration. **It is only safe if beacon is unreachable except through
that proxy** — bind it to loopback (`HOST=127.0.0.1`).

**Set `TRUST_PROXY_HEADERS=false` when beacon is exposed directly.** Otherwise
any caller can send the header and be told about an address that is not theirs.

With `PROXY_PROTOCOL=true` this is handled automatically: the PROXY header
carries the real client address at the TCP layer, so forwarded headers on that
listener — which come from the client, inside its own TLS session, and are
therefore attacker-controlled — are stripped before routing. You do not need to
change `TRUST_PROXY_HEADERS` for the TLS listener.

## API

### Endpoints

- `GET /` — data for the caller's IP.
- `GET /{ip}` — data for the given IP.

### Content negotiation

`?format=json` or `?format=text` forces a representation. Otherwise an `Accept`
containing `application/json` gets JSON and everything else gets plain text.
Lookups and errors set `Cache-Control: no-cache, must-revalidate`; `/healthz`
sets `no-store`. Responses proxied from the frontend carry whatever Next.js
sets.

### JSON

Missing values are `null` throughout — never `0` or `""`. Top-level
`location` / `network` / `flags` come from the first source, so a simple client
can ignore `sources` entirely. `sources` carries the complete answer from
every provider **that had data for the address** — a provider with no record is
omitted rather than listed empty, so an absent name means either "no data" or
"not enabled". `sources_agree` says whether the listed ones matched.

Agreement is semantic, not textual. Sources are compared on the autonomous
system *number*, so "GOOGLE" against "Google LLC" is not a disagreement, and a
source that knows only the country is treated as a coarser answer that folds
into a more specific one rather than as a conflict. Top-level fields are filled
per field from the first source that has each one, so a source knowing only the
ASN does not blank out a city another source knows.

```
$ curl -s -H 'Accept: application/json' http://localhost/31.77.105.207
{
  "version": 2,
  "ip": "31.77.105.207",
  "family": "ipv4",
  "hostname": "714609.senko.network",
  "location": { "city": "Hong Kong", "region": "Central and Western District",
                "region_code": null, "subdivisions": [ ... ], "postal_code": null,
                "country": "Hong Kong", "country_code": "HK",
                "continent": "Asia", "continent_code": "AS",
                "in_european_union": false,
                "registered_country": null, "registered_country_code": null,
                "latitude": 22.2833, "longitude": 114.15,
                "accuracy_radius_km": null,
                "timezone": "Asia/Hong_Kong",
                "local_time": "2026-09-21T20:23:00+08:00",
                "metro_code": null },
  "network": { "asn": 9304, "asn_org": "HGC Global Communications Limited",
               "asn_label": "AS9304 (HGC Global Communications Limited)" },
  "flags": { "anycast": false, "anonymous_proxy": false, "satellite_provider": false },
  "sources_agree": false,
  "sources": [
    { "source": "MaxMind", "location": { ... }, "network": { ... }, "flags": { ... } },
    { "source": "DB-IP",   "location": { ... }, "network": { ... }, "flags": { ... } }
  ]
}
```

Every entry in `sources` has the same shape as the top-level
`location` / `network` / `flags` trio — a full record, not a diff.

`latitude`/`longitude` are `null` when a source has no location for the
address, which is distinct from a genuine `0, 0`.

#### Legacy shape

```
$ curl -s -H 'Accept: application/json' 'http://localhost/8.8.8.8?v=1'
{"ip":"8.8.8.8","city":"Mountain View","country":"United States","country-code":"US","asn":"AS15169 (Google LLC)"}
```

### Plain text

Space-joined `IP [City] [[CC] Country] [ASN]`, with absent parts omitted. It
stays a single line — it is what `curl beacon` prints, and scripts parse it.

When every source agrees there is nothing to attribute, so the line is exactly
what it always was:

```
$ curl -s http://localhost/8.8.8.8
8.8.8.8 Mountain View [US] United States AS15169 (Google LLC)
```

When sources disagree, each distinct answer carries the sources that reported
it, and the alternatives are separated by ` / `. Location and ASN are grouped
independently, so a disagreement about one does not clutter the other:

```
$ curl -s http://localhost/46.133.0.147
46.133.0.147 Dnipro [UA] Ukraine [MaxMind] / Chernivtsi [UA] Ukraine [DB-IP] AS21497 (PrJSC "VF UKRAINE")

$ curl -s http://localhost/31.77.105.207
31.77.105.207 Hong Kong [HK] Hong Kong [MaxMind] / London [GB] United Kingdom [DB-IP] AS9304 (HGC Global Communications Limited) [MaxMind] / AS213520 (Senko Digital LLC) [DB-IP]
```

### Errors

| Case                                     | Status | Body                                     |
| ---------------------------------------- | ------ | ---------------------------------------- |
| String shaped like IPv4 but out of range | `400`  | `{"detail":"Invalid IP address"}`        |
| Not an IP address at all                 | `404`  | `{"detail":"Invalid IP address format"}` |

## Configuration

Settings are environment variables. Copy `example.env` to `.env`; compose reads
it. Nothing is required: beacon runs on DB-IP alone, which needs no account.
Misconfiguration is rejected at startup rather than at the first request.

| Variable                      | Required | Default   | Description                                                       |
| ----------------------------- | -------- | --------- | ----------------------------------------------------------------- |
| `DBIP_ENABLED`                | no       | `true`    | Use DB-IP Lite. No account required.                              |
| `MAXMIND_ACCOUNT_ID`          | no       | —         | MaxMind account ID. Set together with the license key, or neither.|
| `MAXMIND_LICENSE_KEY`         | no       | —         | MaxMind license key.                                              |
| `GEOIP_UPDATE_INTERVAL_HOURS` | no       | `12`      | How often the backend checks for / downloads fresh databases.     |
| `HOST`                        | no       | `0.0.0.0` | Host interface the backend is published on.                       |
| `PORT`                        | no       | `80`      | Host port the backend is published on (mapped to `8000`).         |
| `TRUST_PROXY_HEADERS`         | no       | `true`    | Believe an inbound `X-Real-IP`. Set `false` when exposed directly.|
| `RDNS_ENABLED`                | no       | `true`    | Resolve the PTR record of the address being looked up.            |
| `RDNS_TIMEOUT_MS`             | no       | `300`     | Per-lookup reverse-DNS timeout.                                   |
| `RDNS_CACHE_TTL_SECONDS`      | no       | `3600`    | How long reverse-DNS results are cached.                          |
| `FRONTEND_URL`                | no       | `http://frontend:3000` | Next.js upstream. Empty disables the page entirely. |
| `LISTEN_ADDR`                 | no       | `:8000`   | Address the backend binds inside the container.                   |
| `TLS_ENABLED`                 | no       | `false`   | Terminate TLS in beacon.                                          |
| `DOMAIN`                      | if TLS   | —         | Comma-separated names to obtain certificates for.                 |
| `CF_API_TOKEN`                | if TLS   | —         | Cloudflare API token with `Zone:Read` + `Zone.DNS:Write`.         |
| `CF_ZONE_TOKEN`               | no       | —         | Optional separate `Zone:Read` token, to scope the one above.      |
| `ACME_EMAIL`                  | no       | —         | Contact address for expiry notices.                               |
| `ACME_CA`                     | no       | production| `staging` while testing — production has strict rate limits.      |
| `TLS_LISTEN_ADDR`             | no       | `:8443`   | Address the TLS listener binds inside the container.              |
| `TLS_PUBLISH`                 | no       | unpublished | Host mapping for the TLS port, e.g. `127.0.0.1:8443:8443`.      |
| `PROXY_PROTOCOL`              | no       | `false`   | Read a PROXY protocol header before the TLS handshake.            |

A free MaxMind GeoLite2 account provides the account ID and license key. It is
worth adding as a second opinion, but beacon works without it.

### Reverse DNS

`GET /{ip}` resolves the PTR record of the address being looked up, which means
a caller chooses the destination of an outbound DNS query. Lookups are capped
at 32 concurrent, deduplicated so a burst for one address makes one query, and
cached — including failures. Over the cap a lookup is skipped rather than
queued, and `hostname` comes back `null`. Set `RDNS_ENABLED=false` to turn it
off entirely.

## Health

`GET /healthz` returns `{"status":"ok","sources":[...]}` once the service is
serving. The container healthcheck runs the binary's own `-healthcheck` flag
against it, because the runtime image has no shell tools.

Databases are downloaded **before** the listener opens, so a first start with
an empty volume is unreachable for as long as the download takes — typically
under a minute. The compose healthcheck allows for that with a `start_period`;
`docker compose up -d` followed immediately by `curl` may get a connection
refused until it finishes. With `TLS_ENABLED=true` the ACME exchange also
completes before either listener opens.

## Quick start

```sh
cp example.env .env
docker compose up -d --build
```

No credentials are needed to start: DB-IP Lite is downloaded on first run.
Watch it come up with `docker compose logs -f backend`, or wait for the
container to report healthy.

With the default `HOST=0.0.0.0` / `PORT=80`:

```sh
curl -s -H 'Accept: application/json' http://localhost/
curl -s http://localhost/1.1.1.1
```

Open `http://localhost/` in a browser for the page (it sends a browser
`User-Agent` and `Accept: text/html`, so it is routed to the frontend; bare
`curl` gets plain text).

## GeoIP data

Each source manages its own files in the data volume and downloads them on
first start if they are missing. A background loop re-checks every
`GEOIP_UPDATE_INTERVAL_HOURS`, using a per-source `.timestamp` marker. A source
may impose a longer floor: DB-IP publishes monthly, so it is never checked more
than once a day however low the interval is set.

**DB-IP Lite** — free, no account, CC-BY 4.0, published monthly at a
month-stamped URL. beacon asks for the current month and falls back to the
previous one when the new files are not out yet.

**MaxMind GeoLite2** — Country, City and ASN, fetched with HTTP basic auth,
streamed while hashing, SHA-256–checked against MaxMind's published checksum.
Archives are capped at 64 members and 512 MiB per database, and the license key
is redacted from any logged URL or error.

Every database is written to a temp file and only then renamed into place, and
readers are swapped under a lock, so a lookup never sees a half-written file
and the service keeps answering during an update. A source that fails to
download is logged and retried on the next tick; the readers already open keep
serving. A source that cannot start at all is dropped rather than taking the
service down, as long as one other source works.

Within a source: ASN is always attempted; the City database supplies location
when it has any, otherwise the Country database supplies the country alone.
DB-IP ships no separate Country edition — its City database already carries
country data — so that fallback applies to MaxMind only.

## TLS

By default beacon speaks plain HTTP and you put your own TLS in front of it.

Setting `TLS_ENABLED=true` makes beacon terminate TLS itself. Certificates come
from ACME over the **DNS-01** challenge via Cloudflare, so nothing needs to
reach beacon to prove ownership — no port 80, no inbound connection. CertMagic
keeps them renewed in-process and swaps them into the running config without a
restart, so there is no cron job and no second process. State lives in the
existing data volume.

This matters for what comes next: TLS fingerprints are computed from the raw
ClientHello, and **a proxy that terminates TLS consumes it — it cannot be
recovered downstream.** To fingerprint, beacon has to own the TLS endpoint.

### Behind an existing reverse proxy

If something already holds `:443` on the host, it must pass the connection
through rather than terminate it. With Caddy that is the `layer4` listener
wrapper — note it attaches to Caddy's *existing* listener, so there is no
second bind on `:443`:

```caddyfile
{
    servers :443 {
        listener_wrappers {
            layer4 {
                @beacon tls sni beacon.example.com
                route @beacon {
                    proxy {
                        proxy_protocol v2
                        upstream 127.0.0.1:8443
                    }
                }
            }
            tls          # everything else terminates here, unchanged
        }
    }
}
```

Set `PROXY_PROTOCOL=true` to match. A TCP-level proxy opens a new connection to
beacon, so without it every visitor would be reported as the proxy.

Three things to get right:

- Remove any site block for beacon's domain. Once layer4 matches its SNI the
  block is unreachable, and you do not want the proxy obtaining a certificate
  for a name beacon issues its own certificate for.
- A listener wrapper is TCP-only. If HTTP/3 is advertised on that server, a
  QUIC connection bypasses it entirely — pin the server to `h1 h2`.
- Keep beacon's TLS port on loopback. With `PROXY_PROTOCOL=true` beacon
  requires the header, so a direct connection is refused rather than
  misattributed, but there is no reason to expose it at all.

## Project layout

```
backend/                  Go service
  main.go                 HTTP server, content negotiation, frontend proxy
  internal/config         env config and validation
  internal/browser        navigation vs. tool detection (Fetch Metadata, UA)
  internal/geoip          providers, registry, refresh loop, mmdb lookups
  internal/rdns           bounded, cached reverse-DNS lookups
  internal/render         JSON (v1/v2) / plain-text response shaping
  internal/tlsserve       ACME DNS-01 certificates, TLS + PROXY listener
frontend/                 Next.js 14 app (App Router, standalone output)
  app/page.jsx            "/" route
  app/[ip]/page.jsx       "/{ip}" route
  app/ip-view.jsx         client component: fetch, copy-to-clipboard, UI
  app/layout.jsx          fonts, metadata
docker-compose.yaml       the three-container stack
.github/workflows         SSH deploy
```

## License

AGPL-3.0. See [LICENSE](LICENSE).
