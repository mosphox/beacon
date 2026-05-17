# beacon

A self-hosted "what's my IP" service. One endpoint returns the caller's public
IP address enriched with GeoIP data (city, country, ASN). The same URL serves a
styled web page to browsers and machine-readable output to everything else,
chosen automatically from the request's `Accept` header.

## Features

- **One URL, three audiences.** Browsers get a Next.js page; `curl` gets plain
  text; clients sending `Accept: application/json` get JSON. No separate API
  path.
- **IP enrichment.** City, country, country code, and ASN (number +
  organization) from MaxMind GeoLite2.
- **Arbitrary lookups.** `GET /` returns the caller's IP; `GET /{ip}` looks up
  any address.
- **Self-maintaining GeoIP data.** Databases are downloaded on first start and
  refreshed on a schedule, each archive SHA-256–verified and installed
  atomically. No database files live in the repo.
- **Zero-downtime refresh.** Database readers are hot-swapped under a lock; the
  service keeps answering during an update.
- **Hardened.** Non-root containers, static CGO-free Go binary, license key
  redacted from logs, archive size/member caps, graceful shutdown.
- **Plain HTTP.** The stack speaks plain HTTP and contains no TLS config. Expose
  it directly or front it with your own TLS-terminating proxy — both work.

## Architecture

Four containers, defined in `docker-compose.yaml`:

| Container            | Image          | Role                                                          |
| -------------------- | -------------- | ------------------------------------------------------------- |
| `beacon-nginx`       | `nginx:alpine` | Public entry point. Routes by `Accept` header.                |
| `beacon-frontend`    | Next.js 14     | The web page (App Router, standalone output). Internal `3000`.|
| `beacon-backend`     | Go 1.25        | IP detection, GeoIP lookups, refresh loop. Internal `8000`.   |
| `beacon-init-volume` | `alpine:3.21`  | One-shot: fixes ownership/permissions on the data volume.     |

Only nginx is published to the host, at `${HOST}:${PORT}` mapped to the
container's port 80. The backend and frontend are only reachable inside the
compose network; the GeoIP databases live on a named volume (`beacon-data`).

### Request routing

`nginx/default.conf` is a thin router:

| Request                                 | Goes to  |
| --------------------------------------- | -------- |
| `Accept` contains `text/html`           | frontend |
| Any other `Accept` (curl, JSON clients) | backend  |
| `/_next/*`, `/favicon.ico`, `/logo.png` | frontend |

The page is itself a backend client: it fetches its own pathname with
`Accept: application/json` and renders the result. A browser hitting `/8.8.8.8`
loads the page, which then calls the backend for that IP's data.

### Client IP

nginx forwards an `X-Real-IP` header, using the inbound `X-Real-IP` if present
and falling back to the connection's `$remote_addr` otherwise. The backend
trusts `X-Real-IP`, falling back to the connection peer when it is absent. So
the real caller address is used when beacon is exposed directly; if you put a
proxy in front, have that proxy set `X-Real-IP`.

## API

### Endpoints

- `GET /` — data for the caller's IP.
- `GET /{ip}` — data for the given IP.

### Content negotiation

The backend checks `Accept`: if it contains `application/json` it returns JSON,
otherwise plain text. Every response sets
`Cache-Control: no-cache, must-revalidate`.

### JSON

Missing fields are `null`. Keys: `ip`, `city`, `country`, `country-code`,
`asn`.

```
$ curl -s -H 'Accept: application/json' http://localhost/8.8.8.8
{"ip":"8.8.8.8","city":"Mountain View","country":"United States","country-code":"US","asn":"AS15169 (GOOGLE)"}
```

### Plain text

Space-joined `IP [City] [[CC] Country] [ASN]`, with absent parts omitted:

```
$ curl -s http://localhost/8.8.8.8
8.8.8.8 Mountain View [US] United States AS15169 (GOOGLE)
```

### Errors

| Case                                     | Status | Body                                     |
| ---------------------------------------- | ------ | ---------------------------------------- |
| String shaped like IPv4 but out of range | `400`  | `{"detail":"Invalid IP address"}`        |
| Not an IP address at all                 | `404`  | `{"detail":"Invalid IP address format"}` |

## Configuration

Settings are environment variables. Copy `example.env` to `.env`; compose reads
it. The MaxMind credentials are required — the backend exits on startup if
either is missing.

| Variable                      | Required | Default   | Description                                                       |
| ----------------------------- | -------- | --------- | ----------------------------------------------------------------- |
| `MAXMIND_ACCOUNT_ID`          | yes      | —         | MaxMind account ID for authenticated GeoLite2 downloads.          |
| `MAXMIND_LICENSE_KEY`         | yes      | —         | MaxMind license key.                                              |
| `GEOIP_UPDATE_INTERVAL_HOURS` | no       | `12`      | How often the backend checks for / downloads fresh databases.     |
| `HOST`                        | no       | `0.0.0.0` | Host interface nginx is published on.                             |
| `PORT`                        | no       | `80`      | Host port nginx is published on (mapped to the container's `80`). |

A free MaxMind GeoLite2 account provides the account ID and license key.

## Quick start

```sh
cp example.env .env
# set MAXMIND_ACCOUNT_ID and MAXMIND_LICENSE_KEY in .env
docker compose up -d --build
```

With the default `HOST=0.0.0.0` / `PORT=80`:

```sh
curl -s -H 'Accept: application/json' http://localhost/
curl -s http://localhost/1.1.1.1
```

Open `http://localhost/` in a browser for the page (it sends `Accept: text/html`,
so it is routed to the frontend; bare `curl` is routed to the backend).

## GeoIP data

The backend manages three GeoLite2 editions — **Country**, **City**, **ASN** —
in the data volume. On startup, if any `.mmdb` is missing it downloads
immediately. A background loop then re-checks every
`GEOIP_UPDATE_INTERVAL_HOURS`, using a `.timestamp` marker to decide whether the
data is stale.

Each edition is fetched with HTTP basic auth (account ID + license key),
streamed while hashing, SHA-256–checked against MaxMind's published checksum,
written to a temp file, and only then renamed into place. Readers are swapped
under a lock so a lookup never sees a half-written database. Archives are capped
at 64 members and 512 MiB per database, and the license key is redacted from any
logged URL or error.

Lookup order per request: ASN is always attempted; if the City database has
data for the address it supplies city + country, otherwise the Country database
supplies country only.

## Project layout

```
backend/                  Go service
  main.go                 HTTP server, routing, IP validation, client-IP logic
  internal/config         env config, MaxMind URLs and data paths
  internal/geoip          download + verify + refresh loop, thread-safe lookups
  internal/render         JSON / plain-text response shaping
frontend/                 Next.js 14 app (App Router, standalone output)
  app/page.jsx            "/" route
  app/[ip]/page.jsx       "/{ip}" route
  app/ip-view.jsx         client component: fetch, copy-to-clipboard, UI
  app/layout.jsx          fonts, metadata
nginx/default.conf        Accept-based router
docker-compose.yaml       the four-container stack
.github/workflows         SSH deploy
```

## License

AGPL-3.0. See [LICENSE](LICENSE).
