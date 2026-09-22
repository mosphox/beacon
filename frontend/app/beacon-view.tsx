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

/**
 * Where this service's source lives, for the AGPL section 13 offer in the
 * footer. A fork that changes the code has to change this too — that is the
 * point of the clause.
 */
const SOURCE_URL = 'https://github.com/mosphox/beacon';

/**
 * Selects the address so it can be copied by hand.
 *
 * The hero is deliberately `user-select: none`, so a range over it selects
 * nothing — the rule has to be lifted for the duration. This is the fallback
 * for every case where the Clipboard API is unavailable or refuses.
 */
function selectAddress(button: HTMLButtonElement | null) {
  const text = button?.querySelector('.ip-text');
  if (!text || !window.getSelection) return;

  const selection = window.getSelection();
  if (!selection) return;

  const el = text as HTMLElement;
  el.style.userSelect = 'text';
  const range = document.createRange();
  range.selectNodeContents(el);
  selection.removeAllRanges();
  selection.addRange(range);
}

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

  function copy() {
    if (!data?.ip) return;

    // The Clipboard API is [SecureContext]: over plain HTTP from anything but
    // localhost, navigator.clipboard is undefined. Selecting the address is the
    // honest fallback — the control still does something, and the user can copy
    // it themselves — rather than a button that silently does nothing.
    if (!navigator.clipboard) {
      selectAddress(addressRef.current);
      return;
    }

    // Fire the snap on the click rather than on the clipboard promise: the
    // acknowledgement should track the press, not the round trip.
    if (!reducedMotion) {
      setSnap(true);
      if (snapTimer.current) clearTimeout(snapTimer.current);
      snapTimer.current = setTimeout(() => setSnap(false), 75);
    }

    navigator.clipboard.writeText(data.ip).then(
      () => setCopied((n) => n + 1),
      // Permission denied, or a policy that forbids it. Falling back to a
      // selection keeps the press meaningful instead of swallowing the failure.
      () => selectAddress(addressRef.current),
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
      // The page has no other heading in this state, and the change from
      // loading to failed happens without a navigation, so nothing would
      // announce it. role="alert" is what tells a screen reader the answer is
      // not coming.
      <main className="state" role="alert">
        <div>
          <h1>Could not reach the lookup service.</h1>
          <p>The connection details could not be fetched. This is usually temporary.</p>
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

      <a className="skip" href="#readout">
        Skip to connection details
      </a>

      <main className="deck">
        <section className="panel hero">
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
              onClick={() => readoutRef.current?.scrollIntoView({ block: 'start' })}
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
          <section className="panel readout-panel" ref={readoutRef}>
            <div className="readout" id="readout" tabIndex={-1}>
              <Connection data={data} />
              <LocationSection data={data} />
              <NetworkSection data={data} />
              <TlsSection data={data} showAll={showAll} onExpand={expand} />
              <Http2Section data={data} />
              <Colophon />
            </div>
          </section>
        ) : null}
      </main>
    </>
  );
}

/**
 * Two obligations, discharged where the people they are owed to can see them.
 *
 * AGPL-3.0 section 13 requires a network service to offer its source to the
 * users interacting with it, which a link in a repository cannot do. MaxMind's
 * GeoLite2 EULA requires its notice verbatim, and DB-IP Lite is CC-BY 4.0,
 * which requires attribution on the output and not only in the README.
 */
function Colophon() {
  return (
    <footer className="colophon">
      <p>
        Beacon is free software under the{' '}
        <a href="https://www.gnu.org/licenses/agpl-3.0.html">AGPL-3.0</a>. The complete
        source for this service is at{' '}
        <a href={SOURCE_URL}>{SOURCE_URL.replace('https://', '')}</a>.
      </p>
      <p>
        This product includes GeoLite2 data created by MaxMind, available from{' '}
        <a href="https://www.maxmind.com">maxmind.com</a>. IP geolocation by{' '}
        <a href="https://db-ip.com">DB-IP</a>, licensed under{' '}
        <a href="https://creativecommons.org/licenses/by/4.0/">CC&nbsp;BY&nbsp;4.0</a>.
      </p>
    </footer>
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

  if (sources.length === 0) {
    return (
      <Section title="Network">
        <p className="note">
          No database has network information for this address. Private and reserved
          ranges belong to no autonomous system.
        </p>
      </Section>
    );
  }

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
          {h2.settings.map((s, i) => (
            <Row
              /* Not keyed on the id alone: RFC 9113 6.5 lets a client repeat a
                 setting, and a client that sends INITIAL_WINDOW_SIZE twice is
                 exactly the kind this page exists to look at. */
              key={`${s.id}-${i}`}
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
