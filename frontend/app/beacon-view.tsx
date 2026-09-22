'use client';

import { animate, useReducedMotion } from 'motion/react';
import { usePathname } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';

import { AkamaiBreakdown, Ja4Breakdown } from './components/fingerprint';
import type { CompareRow } from './components/rows';
import { Chips, Compare, Flag, Group, Row, Rows, Section } from './components/rows';
import type { BeaconResponse, Location, Network } from '@/lib/types';
import { PSEUDO_HEADER_NAMES, SETTING_NAMES } from '@/lib/types';

type Status = 'loading' | 'ready' | 'error';

function placeOf(loc: Location): string {
  const parts = [loc.city, loc.region, loc.country].filter(Boolean);
  return parts.join(', ');
}

function coordsOf(loc: Location): string | null {
  if (loc.latitude === null || loc.longitude === null) return null;
  return `${loc.latitude.toFixed(4)}, ${loc.longitude.toFixed(4)}`;
}

/**
 * Reads the wall clock out of the timestamp as written.
 *
 * `local_time` already carries the target zone's offset, so it is the time *there*. Parsing
 * it into a Date and asking for the hours would convert it to the viewer's zone and label
 * someone else's clock as theirs.
 */
function clockOf(loc: Location): string | null {
  if (!loc.local_time) return null;
  const match = /T(\d{2}):(\d{2})/.exec(loc.local_time);
  return match ? `${match[1]}:${match[2]}` : null;
}

export default function BeaconView() {
  const pathname = usePathname();
  const [data, setData] = useState<BeaconResponse | null>(null);
  const [status, setStatus] = useState<Status>('loading');
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState(0);
  const [snap, setSnap] = useState(false);
  const snapTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [showAll, setShowAll] = useState<Record<string, boolean>>({});
  const [view, setView] = useState<View>('hero');
  const stageRef = useRef<HTMLElement>(null);
  const heroRef = useRef<HTMLElement>(null);
  const readoutRef = useRef<HTMLElement>(null);
  const addressRef = useRef<HTMLButtonElement>(null);
  const reducedMotion = useReducedMotion();

  useEffect(() => {
    let live = true;
    const target = process.env.NEXT_PUBLIC_DATA_URL || pathname || '/';

    fetch(target, { headers: { Accept: 'application/json' }, cache: 'no-store' })
      .then((r) => {
        if (!r.ok) throw new Error(`status ${r.status}`);
        return r.json() as Promise<BeaconResponse>;
      })
      .then((body) => {
        if (!live) return;
        if (!body?.ip) throw new Error('no address in response');
        setData(body);
        setStatus('ready');
      })
      .catch(() => {
        if (live) setStatus('error');
      });

    return () => {
      live = false;
    };
  }, [pathname, attempt]);

  useEffect(() => {
    const root = addressRef.current;
    if (reducedMotion || !root) return;

    // Only the glow is animated. It is flat colour with no clipped background, so a
    // filter on it is safe; pulling it into focus behind the address is what reads as the
    // address resolving out of light. The address itself rides the hero's CSS fade-up.
    const glow = root.querySelector('.ip-glow');
    if (glow) {
      animate(
        glow,
        { opacity: [0, 0.55], filter: ['blur(60px)', 'blur(28px)'] },
        { duration: 1.1, ease: [0.2, 0, 0, 1] },
      );
    }
  }, [reducedMotion, data?.ip]);

  useEffect(() => {
    if (copied === 0) return;
    const t = setTimeout(() => setCopied(0), 2200);
    return () => clearTimeout(t);
  }, [copied]);

  useEffect(() => () => void (snapTimer.current && clearTimeout(snapTimer.current)), []);

  usePull(stageRef, readoutRef, view, setView, status === 'ready' && data !== null);

  // The view that leaves becomes inert, so anything focused inside it would be orphaned
  // and focus would fall back to the document. Move it into the view that arrived instead,
  // whether the change came from a gesture, a key or a button.
  const firstView = useRef(true);
  useEffect(() => {
    if (firstView.current) {
      firstView.current = false;
      return;
    }
    // The view container, not the control inside it. Focusing the address would paint a
    // ring around the hero for someone who only scrolled, and landing on the container
    // puts the tab sequence at the start of what is now on screen.
    const target = view === 'readout' ? readoutRef.current : heroRef.current;
    target?.focus({ preventScroll: true });
  }, [view]);

  function copy() {
    if (!data?.ip || !navigator.clipboard) return;

    // Fire the snap on the click rather than on the clipboard promise: the
    // acknowledgement should track the press, not the round trip.
    if (!reducedMotion) {
      setSnap(true);
      if (snapTimer.current) clearTimeout(snapTimer.current);
      snapTimer.current = setTimeout(() => setSnap(false), 75);
    }

    navigator.clipboard.writeText(data.ip).then(
      () => setCopied((n) => n + 1),
      () => {},
    );
  }

  function expand(key: string) {
    setShowAll((prev) => ({ ...prev, [key]: true }));
  }

  function retry() {
    setStatus('loading');
    setData(null);
    setAttempt((n) => n + 1);
  }

  if (status === 'error') {
    return (
      <main className="state">
        <div>
          <p>Could not reach the lookup service.</p>
          <button type="button" className="retry" onClick={retry}>
            Try again
          </button>
        </div>
      </main>
    );
  }

  const isRoot = !pathname || pathname === '/';
  const loading = status === 'loading' || data === null;

  return (
    <>
      {copied > 0 ? (
        <output className="copied" key={copied} aria-live="polite">
          Copied to clipboard
        </output>
      ) : null}

      {/* A button, not a link: there is no longer a place to navigate to. The readout
          is a view this switches to, and the hidden one is inert, so an in-page anchor
          would point at something no one can reach. */}
      <button type="button" className="skip" onClick={() => setView('readout')}>
        Skip to connection details
      </button>

      <main className="stage" ref={stageRef} data-view={view}>
        {/* The gauge for the pull. It grows from the edge you are pulling toward, so the
            threshold is something you can see coming rather than a cliff. */}
        <span className="pull-gauge" aria-hidden="true" />

        <section className="view hero" ref={heroRef} tabIndex={-1} inert={view !== 'hero'}>
          <div
            className="hero-inner"
            /* Genuinely runtime-computed: the whole hero is sized from the address's
               character count. */
            style={
              data ? ({ '--ip-chars': data.ip.length } as React.CSSProperties) : undefined
            }
          >
            <p className="hero-label">{isRoot ? 'Your IP address' : 'IP lookup'}</p>

            <h1 className="hero-h1">
              {loading ? (
                <span className="ip">
                  <span className="ip-stack">
                    <span className="ip-text">· · ·</span>
                  </span>
                </span>
              ) : (
                <button
                  ref={addressRef}
                  type="button"
                  className={snap ? 'ip snap' : 'ip'}
                  onClick={copy}
                  translate="no"
                  /* The split glyphs are decorative once the label carries the address;
                     without this some screen readers spell it out character by character. */
                  aria-label={`Copy IP address ${data.ip}`}
                >
                  <span className="ip-bracket left" aria-hidden="true">
                    [
                  </span>
                  <span className="ip-stack">
                    <span className="ip-glow" aria-hidden="true">
                      {data.ip}
                    </span>
                    <span className="ip-text" aria-hidden="true">
                      {data.ip}
                    </span>
                  </span>
                  <span className="ip-bracket right" aria-hidden="true">
                    ]
                  </span>
                </button>
              )}
            </h1>

            {!loading ? <HeroPlace data={data} /> : null}
          </div>

          {!loading ? (
            <button
              type="button"
              className="scroll-cue"
              onClick={() => setView('readout')}
              aria-label="Scroll to connection details"
            >
              <svg width="20" height="20" viewBox="0 0 12 12" aria-hidden="true">
                <path
                  d="M2 4.5 6 8.5 10 4.5"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
          ) : null}
        </section>

        {!loading ? (
          <section
            className="view readout-panel"
            ref={readoutRef}
            tabIndex={-1}
            inert={view !== 'readout'}
          >
            <div className="readout" id="readout">
              <Connection data={data} />
              <LocationSection data={data} />
              <NetworkSection data={data} />
              <TlsSection data={data} showAll={showAll} onExpand={expand} />
              <Http2Section data={data} />
            </div>
          </section>
        ) : null}
      </main>
    </>
  );
}

function HeroPlace({ data }: { data: BeaconResponse }) {
  const { location, network } = data;
  const place = placeOf(location);

  return (
    <>
      {place ? (
        <p className="hero-location">
          {location.city ? <span className="city">{location.city}</span> : null}
          {location.city && location.country ? ', ' : null}
          {location.country ? (
            <span className="country">
              {location.country}
              {location.country_code ? ` (${location.country_code})` : ''}
            </span>
          ) : null}
        </p>
      ) : (
        <p className="hero-location unknown">No location for this address</p>
      )}
      {network.asn_label ? <p className="hero-asn">{network.asn_label}</p> : null}
    </>
  );
}

/* ---------------------------------------------------------------- the pull */

/**
 * How much scrolling, inside the last second, moves you to the other view.
 * Roughly six notches of a mouse wheel, or one decisive trackpad swipe.
 */
const PULL_THRESHOLD = 600;

/** The window the pull is measured over. Older input has simply stopped counting. */
const PULL_WINDOW = 1000;

/**
 * Touch counts for more than its raw travel. A finger moves one pixel per pixel, while a
 * wheel notch is worth a hundred, so at the same threshold a phone would need most of the
 * screen swiped inside a second. This puts it at roughly half a screen.
 */
const TOUCH_GAIN = 1.6;

/** How long the view change takes; must match the transition in globals.css. */
const SWAP_MS = 520;

/** A line and a page, for wheels that report their delta in them rather than pixels. */
function wheelPixels(e: WheelEvent, page: number): number {
  if (e.deltaMode === 1) return e.deltaY * 40;
  if (e.deltaMode === 2) return e.deltaY * page;
  return e.deltaY;
}

type View = 'hero' | 'readout';

/**
 * Switching views is a gesture, not a scroll.
 *
 * There is no scroll position between the two views to be in the middle of — the hero
 * does not scroll at all, and the readout scrolls normally within itself. What moves you
 * between them is a *rate*: the script sums the scrolling you did in the last second,
 * continuously, and once that sum passes PULL_THRESHOLD it plays the change. Input older
 * than the window stops counting, so the sum falls on its own and a slow drift never
 * arrives. It has to be one committed push.
 *
 * The pull is published as `--pull`, 0 to 1, for the gauge to draw. Feedback is the whole
 * reason to measure a rate rather than a total: a threshold you cannot see coming is
 * indistinguishable from a page that has stopped responding.
 *
 * It listens in the two places a change is what the scrolling could mean: anywhere on the
 * hero, and on the readout only when it is already at its own top.
 */
function usePull(
  stageRef: React.RefObject<HTMLElement | null>,
  readoutRef: React.RefObject<HTMLElement | null>,
  view: View,
  setView: (v: View) => void,
  enabled: boolean,
) {
  // The listeners outlive any one view, so they read it from a ref rather than being
  // torn down and re-registered every time it changes.
  const viewRef = useRef(view);
  useEffect(() => {
    viewRef.current = view;
  }, [view]);

  useEffect(() => {
    const stage = stageRef.current;
    if (!stage || !enabled) return;

    /** Recent input, newest last, as [timestamp, pixels]. */
    let samples: [number, number][] = [];
    let raf = 0;
    let swapping = false;
    let touchY: number | null = null;

    const setPull = (v: number) => {
      stage.style.setProperty('--pull', v.toFixed(3));
      stage.classList.toggle('pulling', v > 0.02);
    };

    /** Drops what has aged out and returns what is left. */
    const total = (now: number) => {
      samples = samples.filter(([t]) => now - t < PULL_WINDOW);
      return samples.reduce((sum, [, d]) => sum + d, 0);
    };

    // The window has to keep draining while the input has stopped, or the gauge would
    // freeze wherever the last event left it.
    const drain = () => {
      const left = total(performance.now());
      setPull(Math.min(1, left / PULL_THRESHOLD));
      raf = left > 0 ? requestAnimationFrame(drain) : 0;
    };

    const swap = (to: View) => {
      swapping = true;
      samples = [];
      setPull(0);
      setView(to);
      window.setTimeout(() => {
        swapping = false;
      }, SWAP_MS);
    };

    /** Is a push in this direction something this view can answer? */
    const accepts = (down: boolean) => {
      if (viewRef.current === 'hero') return down;
      if (!down) {
        const readout = readoutRef.current;
        return !!readout && readout.scrollTop <= 0;
      }
      return false;
    };

    const push = (delta: number) => {
      if (swapping) return false;
      const down = delta > 0;
      if (!accepts(down)) {
        // Pushing the other way is a change of mind, not progress toward anything.
        if (samples.length > 0) {
          samples = [];
          setPull(0);
        }
        return false;
      }

      const now = performance.now();
      samples.push([now, Math.abs(delta)]);
      const sum = total(now);

      if (sum >= PULL_THRESHOLD) {
        swap(viewRef.current === 'hero' ? 'readout' : 'hero');
        return true;
      }

      setPull(sum / PULL_THRESHOLD);
      if (!raf) raf = requestAnimationFrame(drain);
      return true;
    };

    const onWheel = (e: WheelEvent) => {
      const claimed = push(wheelPixels(e, stage.clientHeight));
      // Only swallow what the gesture is actually using; the readout must stay
      // ordinarily scrollable everywhere else.
      if (claimed || swapping) e.preventDefault();
    };

    const onTouchStart = (e: TouchEvent) => {
      touchY = e.touches[0]?.clientY ?? null;
    };

    const onTouchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY;
      if (y === undefined || touchY === null) return;
      const delta = touchY - y;
      touchY = y;
      if (push(delta * TOUCH_GAIN)) e.preventDefault();
    };

    const onTouchEnd = () => {
      touchY = null;
    };

    // Arrow and page keys do the same job without having to earn it. They are already
    // deliberate, and making someone hammer a key to cross a threshold would be absurd.
    const onKeyDown = (e: KeyboardEvent) => {
      if (swapping || e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;

      const forward = e.key === 'PageDown' || e.key === 'ArrowDown' || e.key === ' ';
      const back = e.key === 'PageUp' || e.key === 'ArrowUp' || e.key === 'Home';

      if (viewRef.current === 'hero' && forward) {
        e.preventDefault();
        swap('readout');
      } else if (viewRef.current === 'readout' && back) {
        const readout = readoutRef.current;
        if (readout && readout.scrollTop <= 0) {
          e.preventDefault();
          swap('hero');
        }
      }
    };

    stage.addEventListener('wheel', onWheel, { passive: false });
    stage.addEventListener('touchstart', onTouchStart, { passive: true });
    stage.addEventListener('touchmove', onTouchMove, { passive: false });
    stage.addEventListener('touchend', onTouchEnd, { passive: true });
    window.addEventListener('keydown', onKeyDown);

    return () => {
      stage.removeEventListener('wheel', onWheel);
      stage.removeEventListener('touchstart', onTouchStart);
      stage.removeEventListener('touchmove', onTouchMove);
      stage.removeEventListener('touchend', onTouchEnd);
      window.removeEventListener('keydown', onKeyDown);
      if (raf) cancelAnimationFrame(raf);
      stage.style.removeProperty('--pull');
      stage.classList.remove('pulling');
    };
  }, [stageRef, readoutRef, setView, enabled]);
}

/* --------------------------------------------------------------- connection */

function Connection({ data }: { data: BeaconResponse }) {
  const { flags } = data;
  const marks = [
    flags.anycast ? 'anycast' : null,
    flags.anonymous_proxy ? 'anonymous proxy' : null,
    flags.satellite_provider ? 'satellite' : null,
  ].filter((m): m is string => m !== null);

  return (
    <Section title="Connection">
      <Group>
        <Rows>
          <Row label="Address" value={data.ip} />
          <Row
            label="Family"
            value={data.family === 'ipv6' ? 'IPv6' : data.family === 'ipv4' ? 'IPv4' : null}
          />
          <Row label="Reverse DNS" value={data.hostname} absent="no PTR record" />
          <Row
            label="Marked as"
            /* Stated rather than hidden. "Nothing unusual" is the answer most addresses
               get, and it is worth saying out loud. */
            absent="nothing unusual"
            value={
              marks.length > 0 ? (
                <>
                  {marks.map((m) => (
                    <Flag key={m} on>
                      {m}
                    </Flag>
                  ))}
                </>
              ) : null
            }
          />
        </Rows>
      </Group>
    </Section>
  );
}

/* ----------------------------------------------------------------- location */

/**
 * The location fields, in two blocks.
 *
 * `place` is what a database was asked for; `admin` is what follows from the country it
 * named. Splitting them matters when the sources disagree — two databases can put you in
 * different cities while agreeing on everything in the second block, and running all nine
 * fields together hid that.
 *
 * Each field is a function of a Location so the same list drives both the single-source
 * table and the side-by-side comparison. One definition, two renderings.
 */
type Field<T> = { label: string; of: (v: T) => string | null };

const PLACE_FIELDS: Field<Location>[] = [
  { label: 'Place', of: (l) => placeOf(l) || null },
  { label: 'Postal code', of: (l) => l.postal_code },
  { label: 'Coordinates', of: (l) => coordsOf(l) },
  {
    label: 'Accuracy',
    of: (l) => (l.accuracy_radius_km === null ? null : `±${l.accuracy_radius_km}\u00A0km`),
  },
  { label: 'Time zone', of: (l) => l.timezone },
  { label: 'Local time', of: (l) => (clockOf(l) ? `${clockOf(l)} there` : null) },
];

const ADMIN_FIELDS: Field<Location>[] = [
  { label: 'Continent', of: (l) => l.continent },
  {
    label: 'European Union',
    of: (l) => (l.country_code ? (l.in_european_union ? 'yes' : 'no') : null),
  },
  {
    label: 'Registered to',
    of: (l) =>
      l.registered_country
        ? `${l.registered_country}${
            l.registered_country_code ? ` (${l.registered_country_code})` : ''
          }`
        : null,
  },
];

function compareRows<T>(fields: Field<T>[], subjects: T[]): CompareRow[] {
  return fields.map((f) => ({ label: f.label, values: subjects.map(f.of) }));
}

function plainRows<T>(fields: Field<T>[], subject: T) {
  return fields.map((f) => <Row key={f.label} label={f.label} value={f.of(subject)} />);
}

function LocationSection({ data }: { data: BeaconResponse }) {
  const { location, sources, sources_agree: agree } = data;

  if (sources.length === 0) {
    return (
      <Section title="Location">
        <p className="note">
          No database has a location for this address. Private and reserved ranges are not
          geolocated.
        </p>
      </Section>
    );
  }

  const names = sources.map((s) => s.source);
  const places = sources.map((s) => s.location);
  const multiple = sources.length > 1;

  return (
    <Section
      title="Location"
      note={multiple ? `${sources.length} sources ${agree ? 'agree' : 'disagree'}` : names[0]}
      noteTone={multiple ? (agree ? 'agree' : 'differ') : undefined}
      intro={
        multiple && !agree
          ? 'The databases place this address differently. Both answers stand as reported; neither is corrected against the other, and the fields they differ on are marked.'
          : undefined
      }
    >
      <Group caption="Where it puts you">
        {multiple ? (
          <Compare columns={names} rows={compareRows(PLACE_FIELDS, places)} />
        ) : (
          <Rows>{plainRows(PLACE_FIELDS, location)}</Rows>
        )}
      </Group>

      <Group caption="Country and registry">
        {multiple ? (
          <Compare columns={names} rows={compareRows(ADMIN_FIELDS, places)} />
        ) : (
          <Rows>{plainRows(ADMIN_FIELDS, location)}</Rows>
        )}
      </Group>
    </Section>
  );
}

/* ------------------------------------------------------------------ network */

const NETWORK_FIELDS: Field<Network>[] = [
  { label: 'Autonomous system', of: (n) => (n.asn === null ? null : `AS${n.asn}`) },
  { label: 'Operator', of: (n) => n.asn_org },
];

function NetworkSection({ data }: { data: BeaconResponse }) {
  const { network, sources } = data;
  const multiple = sources.length > 1;
  const orgs = new Set(sources.map((s) => s.network.asn_org).filter(Boolean));
  const asns = new Set(sources.map((s) => s.network.asn).filter((a) => a !== null));

  return (
    <Section title="Network">
      <Group
        note={
          orgs.size > 1 && asns.size === 1
            ? 'The sources agree on the network but spell its operator differently. That is a naming difference, not a disagreement about where the traffic goes.'
            : undefined
        }
      >
        {multiple ? (
          <Compare
            columns={sources.map((s) => s.source)}
            rows={compareRows(
              NETWORK_FIELDS,
              sources.map((s) => s.network),
            )}
          />
        ) : (
          <Rows>{plainRows(NETWORK_FIELDS, network)}</Rows>
        )}
      </Group>
    </Section>
  );
}

/* ---------------------------------------------------------------------- TLS */

function TlsSection({
  data,
  showAll,
  onExpand,
}: {
  data: BeaconResponse;
  showAll: Record<string, boolean>;
  onExpand: (key: string) => void;
}) {
  const tls = data.tls;

  if (!tls) {
    return (
      <Section title="TLS">
        <p className="note">
          No fingerprint for this connection. Beacon computes these from the TLS handshake, and
          this request did not arrive over TLS that beacon terminated itself — a proxy in front
          consumes the handshake, and it cannot be recovered afterwards.
        </p>
      </Section>
    );
  }

  const hello = tls.client_hello;

  return (
    <Section
      title="TLS"
      intro="How your client opened the connection. The same browser on the same machine produces the same values, which is what makes them useful for identifying software."
    >
      <Group
        caption="Fingerprints"
        note={
          tls.ja4 === tls.ja4_o
            ? 'The sorted and wire-order forms match, so this client sends its extensions in a stable order.'
            : 'The sorted and wire-order forms differ, so this client shuffles its extension order on every connection. That is why the sorted form exists.'
        }
      >
        <Ja4Breakdown ja4={tls.ja4} />
        <Rows>
          <Row label="JA4" value={tls.ja4} />
          <Row label="JA4 (wire order)" value={tls.ja4_o} />
          <Row label="JA3" value={tls.ja3_hash} />
          <Row label="JA3 (sorted)" value={tls.ja3n_hash} />
        </Rows>
      </Group>

      <Group caption="What your client offered">
        <Rows>
          <Row label="Version" value={hello.version} />
          <Row label="Server name" value={hello.server_name} absent="none sent" />
          <Row label="ALPN" value={hello.alpn.length > 0 ? hello.alpn.join(', ') : null} />
          <Row label="GREASE" value={hello.grease ? 'present' : 'absent'} />
          {hello.truncated ? (
            <Row
              label="Client hello"
              value="too large to show in full; the fingerprints above still cover all of it"
            />
          ) : (
            <>
              <Row
                label="Cipher suites"
                value={
                  <Chips
                    items={hello.cipher_suites}
                    expanded={!!showAll.ciphers}
                    onExpand={() => onExpand('ciphers')}
                    label="cipher suites"
                  />
                }
              />
              <Row
                label="Extensions"
                value={
                  <Chips
                    items={hello.extensions}
                    expanded={!!showAll.extensions}
                    onExpand={() => onExpand('extensions')}
                    label="extensions"
                  />
                }
              />
              <Row
                label="Groups"
                value={
                  <Chips
                    items={hello.supported_groups}
                    expanded
                    onExpand={() => {}}
                    label="groups"
                  />
                }
              />
            </>
          )}
        </Rows>
      </Group>

      {tls.negotiated ? (
        <Group caption="What the two sides agreed on">
          <Rows>
            <Row label="Version" value={tls.negotiated.version} />
            <Row label="Cipher" value={tls.negotiated.cipher_suite} />
            <Row label="Key exchange" value={tls.negotiated.key_exchange} />
            <Row label="Protocol" value={tls.negotiated.alpn} />
            <Row label="Resumed" value={tls.negotiated.resumed ? 'yes' : 'no'} />
          </Rows>
        </Group>
      ) : null}
    </Section>
  );
}

/* ------------------------------------------------------------------- HTTP/2 */

function Http2Section({ data }: { data: BeaconResponse }) {
  const h2 = data.http2;

  if (!h2) {
    return (
      <Section title="HTTP/2">
        <p className="note">
          This request used HTTP/1.1, which has no equivalent fingerprint — the values below
          come from the frames an HTTP/2 client sends before its first request.
        </p>
      </Section>
    );
  }

  const order = h2.pseudo_header_order.map((c) => PSEUDO_HEADER_NAMES[c] ?? `:${c}`);

  return (
    <Section
      title="HTTP/2"
      intro="The frames your client sent before asking for anything. Libraries and browsers differ here more than you might expect."
    >
      <Group caption="Fingerprint">
        <AkamaiBreakdown akamai={h2.akamai} />
        <Rows>
          <Row label="Hash" value={h2.akamai_hash} />
        </Rows>
      </Group>

      <Group
        caption="Settings the client sent"
        note={
          h2.settings.length === 0
            ? 'This client sent an empty SETTINGS frame, which is legal and unusual.'
            : undefined
        }
      >
        <Rows>
          {h2.settings.map((s) => (
            <Row
              key={s.id}
              labelMono
              label={SETTING_NAMES[s.id] ?? `SETTING_${s.id}`}
              value={s.value.toLocaleString()}
            />
          ))}
        </Rows>
      </Group>

      <Group caption="Everything else in the preamble">
        <Rows>
          <Row
            label="Window update"
            value={h2.window_update > 0 ? `${h2.window_update.toLocaleString()}\u00A0bytes` : null}
            absent="none sent"
          />
          <Row
            label="Priority frames"
            value={h2.priorities.length > 0 ? String(h2.priorities.length) : null}
            absent="none sent"
          />
          <Row
            label="Pseudo-header order"
            value={order.length > 0 ? order.join(' ') : null}
          />
        </Rows>
      </Group>
    </Section>
  );
}
