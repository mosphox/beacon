# beacon — frontend

The web page for beacon, a self-hosted "what's my IP" service. It shows a visitor their own
address at size, and below the fold everything the server could observe about their
connection: location from multiple sources, network, TLS and HTTP/2 client fingerprints.

## Stack

- Next.js 16 (App Router), React 19, TypeScript strict
- Styling: hand-written CSS in `app/globals.css`. **No Tailwind, no shadcn** — deliberate.
  The page is an art-directed readout, not an app shell; a utility framework would not
  earn its bundle here.
- Fonts via `next/font` (Outfit, JetBrains Mono). No images beyond `public/logo.png`.
- Package manager: npm
- Deploys as a standalone build inside the `beacon-frontend` container; the Go backend
  reverse-proxies browser navigations to it.

## Read first

- `DESIGN.md` before writing or changing any UI. It is the source of truth for direction,
  type, color, spacing, motion, and the two treatments that carry the design. If it does
  not cover something, add it there first, then use it.
- Version-matched Next.js docs are in `node_modules/next/dist/docs/`. Read the relevant
  page before using a Next.js API you are not certain about. Do not write Next.js from
  memory — `params`/`searchParams`/`cookies()`/`headers()` are async, `middleware.ts` is now
  `proxy.ts`, and `next lint` no longer exists.

## How this page gets its data — read before "fixing" it

The browser fetches its **own pathname** with `Accept: application/json`. In production the
Go backend serves the page and the data at the same URL and picks by content negotiation,
so `/8.8.8.8` returns HTML to a navigating browser and JSON to that same page's `fetch`.

This is a client-side fetch in a `useEffect`, which the usual rule forbids. It is correct
here and must stay: the payload describes **the visitor's own connection** — their address,
their TLS fingerprint, their HTTP/2 frames. A Server Component fetch would describe the
Next.js server's connection to the backend instead, which is a different machine and a
different TLS session. There is no server-side way to obtain this data.

`NEXT_PUBLIC_DATA_URL` overrides the fetch target; it exists for local work against a
fixture.

## Rules

- Server Components by default. `"use client"` only on the leaves that need state or
  browser APIs — in practice the fetch/copy component and nothing else.
- `params`, `searchParams`, `cookies()`, `headers()` are async. Await them.
- No `any`. No `@ts-ignore`. No `eslint-disable` without a comment saying why.
- No `<img>` (use `next/image`). No `<a href="/internal">` (use `next/link`).
- No inline `style={{}}` except for genuinely runtime-computed values.
- The API response is deeply nullable. Model it honestly in `lib/types.ts` and let the
  types force the null handling; do not paper over it with `?? ""`, which turns "the
  database had no answer" into "the answer is empty".
- Values a server measured are monospaced; words a human wrote are not. See `DESIGN.md`.
- Every interactive element is keyboard reachable with a visible focus ring.
- Data values in the readout are selectable. The page sets `user-select: none` broadly so
  the layout does not feel like a document, but anything someone might copy — addresses,
  hashes, fingerprints — must opt back in there. A hash you cannot select is useless.
- The hero address is the exception and is deliberately **not** selectable. It is a button
  whose whole purpose is copying, and a drag-select fights the click. The same address
  appears as a selectable row in the readout for anyone who wants to take it by hand, so
  nothing is actually lost.
- Mobile first. Check 375px before 1280px.

## Definition of done

1. `next build`, `tsc --noEmit` and lint all pass. The `nextjs-guard` hook enforces this at
   the end of every turn; do not work around it.
2. Looked at in the browser at 375 / 768 / 1280.
3. Tabbed through with the keyboard.
4. Ran the `web-design-guidelines` skill on the changed files and fixed what it found.

## Commands

```bash
npm run dev        # http://localhost:3099, with the mock API
npm run build
npm run typecheck  # tsc --noEmit
npm run lint
```

`npm run dev` serves the page and a mock backend on the same port, reproducing production's
content negotiation: a browser navigation gets the page, a `fetch` with
`Accept: application/json` gets generated fixture data. That is what `proxy.ts` is for. It
is a development convenience and is inert in a production build.
