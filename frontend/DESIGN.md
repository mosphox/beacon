# Design system

Beacon's visual identity already existed before this document; it is recorded here rather
than invented. Values are decisions — components must not introduce alternatives.

## Direction

An instrument readout, not a marketing page. Beacon tells you what a server learns about
your connection the moment you arrive, and the audience reads hex for a living. The
reference is a spectrum analyser or a packet capture pane: dark, precise, monospaced,
everything aligned to a grid you can scan down. The one adjective that must be true of
every screen is **legible under scrutiny** — someone will copy a hash out of this page and
paste it into a threat-intel search. The one that must never be true is **decorative**: no
element exists unless it carries data or the structure of data.

The page has two movements. The first viewport is the answer to the question people came
with — their IP, at size, and nothing competing with it. Everything else is below the fold
for people who want it, and it is dense on purpose.

Not this: identical rounded cards with the same shadow under each, a tracked-out ALL-CAPS
label above every section, a fade-and-slide-up as each section scrolls into view, `→`
appended to links.

## Typography

Two families, already established, clearly distinct in role: Outfit is the voice, JetBrains
Mono is the data. Every value a server measured is monospaced; everything a human wrote is
not. That split is the whole typographic system and it should be followed literally.

| Role | Family | Size / line-height | Weight | Tracking |
| --- | --- | --- | --- | --- |
| IP (hero) | JetBrains Mono | `--hero-scale` / 1.1 | 700 | −0.02em |
| Hero label | Outfit | 0.22 × scale, 11–14px | 300 | 0.3em, uppercase |
| Section heading | Outfit | 1.125rem / 1.3 | 600 | −0.01em |
| Row label | Outfit | 0.8125rem / 1.4 | 300 | 0 |
| Row value | JetBrains Mono | 0.875rem / 1.5 | 400 | 0 |
| Fingerprint | JetBrains Mono | `clamp(0.75rem, 2.2vw, 1rem)` / 1.6 | 400 | 0 |
| Annotation | Outfit | 0.75rem / 1.4 | 300 | 0 |

The hero is sized from a single value, `--hero-scale`, derived from the address's own
character count: `clamp(1rem, 125vw/chars, 6rem)`. The label, location and ASN lines are
fractions of it (0.22, 0.34, 0.26), each bounded so they stay legible on a small screen
and never overgrow on a large one. This matters because the address shrinks hard for a
long IPv6 on a narrow screen — at fixed sizes the location line ended up 0.85× the
address and the hierarchy collapsed. It is now 0.55× at worst and 0.21× on a desktop.

The tracked uppercase label is kept **only** in the hero, where it is part of the existing
brand. Section headings below the fold are sentence case; repeating the hero's treatment on
every section is the template tell this design avoids.

Scale ratio 1.2 from a 16px base. Body measure caps at 68ch; data rows are not prose and
may run wider.

## Color

Inherited from the existing page. Dark only — there is no light mode, and adding one is a
design decision, not a chore.

| Token | Value | Use |
| --- | --- | --- |
| `--bg-0` | `#0f0f23` | gradient stop, page top |
| `--bg-1` | `#1a1a2e` | gradient stop, middle |
| `--bg-2` | `#16213e` | gradient stop, bottom |
| `--rule` | `rgb(255 255 255 / 0.07)` | hairlines between rows |
| `--ink` | `#e0e0e0` | primary text, values |
| `--ink-2` | `#9ca3af` | secondary text |
| `--ink-3` | `#6b7280` | labels, captions |
| `--ink-4` | `#4b5563` | hints, disabled |
| `--accent` | `#a78bfa` | the one highlight: emphasised values |
| `--cy` `--vi` `--pk` | `#00d4ff` `#7c3aed` `#f472b6` | the hero IP gradient, nowhere else |
| `--ok` | `#10b981` | sources agree, copy confirmation |
| `--warn` | `#f59e0b` | sources disagree |

Four atmospheric radial washes — teal, violet, magenta, green — sit fixed behind
everything over a four-stop base, so the corners differ from each other rather than being
one navy ramp. They are the only decoration in the system and they do not scroll.

The cyan→violet→pink gradient belongs to the address and to nothing else. Reusing it on
headings or buttons would spend the page's one memorable moment on furniture.

Contrast: `--ink` on `--bg-1` is 12.8:1, `--ink-2` 6.9:1, `--ink-3` 4.6:1. `--ink-4` is for
non-essential hints only and never carries information on its own.

## Spacing and layout

Base unit 4px; scale 4 8 12 16 24 32 48 64 96. Nothing between.

Single column, left-aligned below the fold, centred in the hero. Content max-width 720px.
Gutters 16px mobile, 24px tablet, 32px desktop. Section rhythm 48px mobile, 64px desktop.

Rows are a two-column grid: a label column that right-aligns against the hairline, and a
value column. On mobile the grid collapses to stacked label-over-value — the label column
would otherwise squeeze values onto three lines.

## Shape

Radius 4px on the few interactive surfaces, 2rem on the copy toast only (it is a pill and
it already exists). No radius on rows or sections, because they are not objects — they are
ruled lines in a table.

Hairlines over shadows. The only shadow in the system is under the copy toast, which
genuinely floats.

## Motion

One orchestrated moment: the hero fades up once on load, and the glow behind the address
pulls from a wide blur into focus. Nothing else animates on scroll, and the address's
gradient no longer drifts — the arrival is the moment.

**The address itself must never be animated directly.** It carries a
`background-clip: text` gradient, and anything that gives it or its children a painting
context of their own — a per-glyph opacity, a transform, a mask — stops the clipped
background compositing, and the address renders as a fragment or as nothing at all. The
failure is width-dependent, so it will look correct at one address length and break at
another. Animate the hero wrapper or the blurred glow copy instead; both are safe.

Hover is answered by brackets closing in around the address, the way a shell prompt marks
a value — not by the address moving or growing. They hold their space at rest, so
revealing them shifts nothing, and the address stays exactly where the eye left it. The
glow handles the entrance and then stops participating.

Durations 120ms micro, 200ms standard. Animate `opacity` and `transform` only.
`prefers-reduced-motion` removes the gradient drift and the fade, and is already honoured
globally.

## The two treatments that carry this design

**The address is the page's one piece of spectacle.** It is set from its own character
count so a 39-character IPv6 address and a short IPv4 one both fill the hero without
wrapping, over a blurred duplicate of itself that does the glowing. A blurred copy rather
than stacked text-shadows: softer, cheaper, and it leaves the crisp copy sharp.

**Fingerprints are structured, so show the structure.** A JA4 string is not a blob — it is
`t13d4907h2` (version, SNI, counts, ALPN) `_` a truncated hash of the cipher list `_` a
truncated hash of extensions and signature algorithms. The page renders those three
segments separated, with the prefix decomposed underneath. This is the one place boldness
is spent, and it is specific to beacon: it only makes sense because the server computed the
value and knows what each part means.

**Disagreement is shown, never resolved.** Where two GeoIP sources differ, both claims
appear side by side, each attributed, marked with `--warn`. Beacon is the only service that
does this; collapsing it to a single answer would discard the reason it exists.

## States

Every screen needs all four, designed rather than improvised:

- **Loading** — the hero shows `· · ·` in place of the IP at the same size, so nothing
  reflows when data lands. Detail sections are absent, not skeletonised; they have no fixed
  height to reserve.
- **Empty** — a private or reserved address resolves to no location. Say
  "No location for this address" in the value column rather than hiding the row, so the
  absence is visibly an answer.
- **Error** — "Could not reach the lookup service." plus a retry control. Never a bare
  "unavailable".
- **No TLS** — over plain HTTP the fingerprint sections do not exist. Explain once, in a
  sentence, rather than rendering empty rows: these are absent by architecture, not missing
  data.
