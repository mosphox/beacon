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

The page is two screens, not one long one. The first is the answer people came for — their
IP, at size, with nothing competing — and it does not scroll. The second holds everything
else and scrolls inside itself. The page scrolls between the two and snaps, so each is
arrived at whole rather than half-glimpsed.

Mechanically that is a deck with `scroll-snap-type: y mandatory` and exactly two panels of
one viewport each. The readout's own scrolling happens inside its panel, which is why
there are only ever two snap points — a taller second section would make mandatory
snapping fight every scroll through it.

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
| `--ok` | `#10b981` | sources agree |
| `--warn` | `#f59e0b` | sources disagree |

Four atmospheric radial washes — teal, violet, magenta, green — sit fixed behind
everything over a four-stop base, so the corners differ from each other rather than being
one navy ramp. They are the only decoration in the system and they do not scroll. The two
layers are `--page-wash` and `--page-base`; the page background is defined once, in those
tokens, and anything that ever needs to sit opaquely on top of it repaints them in that
order rather than approximating with a flat colour.

The cyan→violet→pink gradient belongs to the address and to nothing else. Reusing it on
headings or buttons would spend the page's one memorable moment on furniture.

Contrast: `--ink` on `--bg-1` is 12.8:1, `--ink-2` 6.9:1, `--ink-3` 4.6:1. `--ink-4` is for
non-essential hints only and never carries information on its own.

## Spacing and layout

Base unit 4px; scale 4 8 12 16 24 32 48 64 96. Nothing between.

Single column, left-aligned below the fold, centred in the hero. Content max-width 720px.
Gutters 16px mobile, 24px tablet, 32px desktop. Section rhythm 48px mobile, 64px desktop.

The readout is three levels deep and no more: **section → group → table**. A section is a
subject and carries an `h2`. A group is one table with a sentence-case caption saying what
that table is about. Nothing nests further. Sections used to be a single run of rows — TLS
ran to twenty of them — and nothing marked where "what the client offered" ended and "what
the two sides agreed on" began.

Rows are a two-column grid: a label column that right-aligns against the hairline, and a
value column. On mobile the grid collapses to stacked label-over-value — the label column
would otherwise squeeze values onto three lines.

Hairlines go **between** rows, never after the last one. A trailing rule makes a table's
bottom edge identical to its internal ones, and the blocks stop closing.

Nothing in the readout is sticky, and a sticky section heading was tried and taken out. It
would have to hide the rows passing under it, and over a fixed two-layer gradient there is
no honest way: a flat colour reads as a bar pasted on the page, and repainting the page's
own background matches everywhere except along the edges of the heading's own box. Four
screens with no landmark is a real cost, but a bar across every heading is a worse one.

## Shape

Radius tracks the size of the thing it is on, and there are exactly three values.

| Radius | On |
| --- | --- |
| 4px | chips, badges, buttons, focus rings — anything a few characters wide |
| 12px | the fingerprint panels, the only large bordered surfaces on the page |
| full | the copy toast, and nothing else |

4px on a panel the width of the measure reads as a square box with its corners filed
off; 12px reads as a panel. No radius at all on rows or sections, because they are not
objects — they are ruled lines in a table.

The toast is fully rounded, and that is the point of the top of the scale: it is the only
element that floats *above* the page rather than sitting in it, and the shape says so
before the motion does. Nothing else may take that radius, or the distinction stops
meaning anything.

Hairlines over shadows. The only shadow in the system is under the toast, which genuinely
floats; everything else is bounded by a `--rule` line instead.

The toast is tinted glass: the accent violet at a tenth strength over a 20px backdrop
blur, so the page's own gradient comes through it rather than being covered, and it
belongs to this page rather than to the operating system. It carries no status colour at
all — no green dot, no tinted surface. The sentence already says what happened, and a
success marker beside it would spend `--ok` on something that is not a claim about data;
the token is reserved for sources agreeing.

## Motion

One orchestrated moment: the hero fades up once on load, and the glow behind the address
pulls from a wide blur into focus. Nothing else animates on scroll, and the address's
gradient no longer drifts — the arrival is the moment.

**The address element itself must never be animated directly.** It carries a
`background-clip: text` gradient, and anything that gives it or its children a painting
context of their own — a per-glyph opacity, a transform, a mask — stops the clipped
background compositing, and the address renders as a fragment or as nothing at all. The
failure is width-dependent, so it will look correct at one address length and break at
another.

An **ancestor** is a different matter and is safe. A transform on the button around the
address composites fine and the gradient survives it, checked at 7 and 39 characters, at
375px, and with 3D transforms as well as 2D. So are properties of the text that are not
painting contexts — `letter-spacing` animates cleanly. The rule is therefore precise
rather than blanket: move a wrapper, the blurred glow copy, or the button; never
`.ip-text` itself. The glow copy is flat colour and may even be split per character.

Hover is answered by brackets closing in around the address, the way a shell prompt marks
a value — not by the address moving or growing. They hold their space at rest, so
revealing them shifts nothing, and the address stays exactly where the eye left it. The
glow handles the entrance and then stops participating.

A click snaps those same brackets shut against the address and lets them spring back:
45ms in, held 75ms, then the hover state resumes. It is deliberately faster than the
hover reveal — an acknowledgement of a press is not something anyone should watch finish.
It fires on the click rather than on the clipboard promise, so the feedback tracks the
press and not the round trip.

The address gives under that press at the same moment, receding 18px under a 900px
perspective — 900/918, a hair under 2%. It **recedes** rather than scales: the far edges
converge and the line reads as moving away from you instead of changing size. A flat
scale was built first and rejected for exactly that reason; the foreshortening is what
lets so small a movement register as give at all. Both ends of the transition name the
same transform functions, so they interpolate as a list rather than as decomposed
matrices.

In is 55ms and out is 180ms. The asymmetry is the point — a press should feel like a
release, not a round trip — and it is why the address settles a beat after the brackets
have already come back. `/lab` holds the eight alternatives this was chosen from.

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

**Disagreement is shown, never resolved.** Where the GeoIP sources differ, they get a real
table — one column per source, one row per field — because the comparison is
two-dimensional and only lines up as a table. "MaxMind says ±1000 km, DB-IP says ±50 km"
is a sentence you read off a column, not out of a paragraph. Beacon is the only service
that does this; collapsing it to a single answer would discard the reason it exists.

The disagreement is marked on the **row**, as a `--warn` edge against the field name, and
the values stay in `--ink`. Painting the differing values amber was tried first and read
backwards: two databases usually differ on most fields, so nearly every cell came out
amber and the one agreeing row was the thing that stood out. Agreement is the quiet
default and the marker is what you scan for.

This also settles an attribution bug the old layout had: the merged values were listed
unattributed directly under a heading announcing that the sources disagreed, which left no
way to tell whose numbers those were. When there is more than one source, every location
and network field now sits in a column with a name on it.

**A source is compared only on what it covers.** The sources differ in precision, not
just in opinion: MaxMind has postal codes and DB-IP Lite has none for any address; IPLocate
and IPFire know the country and not the city; ip-location-db gives a bare country code.
Each source says what it covers (`provides` in the response), and the comparison honours
it. Otherwise the marker loses its meaning: with five sources, a country-only database's
empty "Coordinates" cell would put a `--warn` edge on nearly every row whether anyone
disagreed or not.

- A source joins a table only if it covers something in it. Country-level sources are not
  columns in "Where it puts you"; a note under that table names them and points to the
  next table, where they are. A source with no network data is not a column under
  "Network".
- A cell outside a source's data is a dash in `--ink-4`, with "not in this source's data"
  for screen readers, and it does not count when deciding whether the row differs. That
  is distinct from the italic "not available" in `--ink-3`, which is a source that covers
  the field answering "nothing for this address" — an answer, and so still a difference.
- Stacked on a phone, where every value already names its source, uncovered cells are
  dropped rather than dashed: a list has no columns to hold in line.
- A row no column covers is not drawn at all, and neither is a table. "Registration"
  exists only while MaxMind or RIPE is configured.
- A place-table source that says nothing past the country gets no column in "Country":
  its Place already shows the country. That is the geofeeds, which give a city, a region
  code and a country code — the network's own word — and a seventh column there would
  squeeze every country name onto two lines.
- A registry places nothing. RIPE names the country an address is registered to — the
  holder's say, which for a VPN or a leased range is nowhere near the visitor — so it is
  compared with MaxMind's registered country in a table of its own, "Registration", and it
  is not counted in "N sources agree", which is about where you are. As a column in
  "Country" it would add a column of dashes to a table already six wide, where names
  already wrap. A registered country that differs is marked on its row like any other
  field.

Country names are shown one way per code — the first source's spelling, or the browser's
own name for a code no source names. A region given as a code alone takes the name a
source uses for the same code in the same country, and stays unnamed otherwise. Values
are compared without case or accents, so a geofeed's plain-ASCII "Malmo" does not mark a
row against "Malmö". IPFire says "United States of America" where DB-IP
says "United States", and comparing the text would present a spelling as a disagreement
about where the visitor is. The JSON keeps every source's own spelling; the table compares
places, not orthography.

The connection's flags are assertions by particular databases, so "Marked as" says whose,
in the annotation style beside the badges. "Nothing unusual" is only said when some source
present actually checks: DB-IP Lite never does, and its silence is not a clean bill.

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
