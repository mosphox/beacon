'use client';

import { animate, useReducedMotion } from 'motion/react';
import { usePathname } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';

import { AkamaiBreakdown, Ja4Breakdown } from './components/fingerprint';
import { Chips, Flag, Row, Rows, Section } from './components/rows';
import type { BeaconResponse, Location, Source } from '@/lib/types';

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
    if (!data?.ip || !navigator.clipboard) return;

    // Fire the snap on the click rather than on the clipboard promise: the
    // acknowledgement should track the press, not the round trip.
    if (!reducedMotion) {
      setSnap(true);
      if (snapTimer.current) clearTimeout(snapTimer.current);
      snapTimer.current = setTimeout(() => setSnap(false), 150);
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

function Connection({ data }: { data: BeaconResponse }) {
  const { flags } = data;
  const anyFlag = flags.anycast || flags.anonymous_proxy || flags.satellite_provider;

  return (
    <Section title="Connection">
      <Rows>
        <Row label="Address" value={data.ip} />
        <Row
          label="Family"
          value={data.family === 'ipv6' ? 'IPv6' : data.family === 'ipv4' ? 'IPv4' : null}
        />
        <Row label="Reverse DNS" value={data.hostname} absent="no PTR record" />
        {anyFlag ? (
          <Row
            label="Marked as"
            value={
              <>
                {flags.anycast ? <Flag on>anycast</Flag> : null}
                {flags.anonymous_proxy ? <Flag on>anonymous proxy</Flag> : null}
                {flags.satellite_provider ? <Flag on>satellite</Flag> : null}
              </>
            }
          />
        ) : null}
      </Rows>
    </Section>
  );
}

function LocationSection({ data }: { data: BeaconResponse }) {
  const { location, sources, sources_agree: agree } = data;
  const multiple = sources.length > 1;

  return (
    <Section
      title="Location"
      note={
        sources.length === 0
          ? undefined
          : multiple
            ? agree
              ? `${sources.length} sources agree`
              : `${sources.length} sources disagree`
            : `1 source`
      }
      noteTone={multiple ? (agree ? 'agree' : 'differ') : undefined}
      intro={
        multiple && !agree
          ? 'Two databases place this address differently. Both answers are shown as reported; neither is corrected against the other.'
          : undefined
      }
    >
      {sources.length === 0 ? (
        <p className="note">
          No database has a location for this address. Private and reserved ranges are not
          geolocated.
        </p>
      ) : multiple ? (
        <div className="rows">
          {sources.map((s) => (
            <Claim key={s.source} source={s} differs={!agree} />
          ))}
        </div>
      ) : null}

      <Rows>
        {!multiple && sources.length > 0 ? (
          <Row label="Place" value={placeOf(location)} />
        ) : null}
        <Row label="Postal code" value={location.postal_code} />
        <Row
          label="Coordinates"
          value={
            coordsOf(location) ? (
              <>
                {coordsOf(location)}
                {location.accuracy_radius_km !== null ? (
                  <span className="unit">{` ±${location.accuracy_radius_km} km`}</span>
                ) : null}
              </>
            ) : null
          }
        />
        <Row
          label="Time zone"
          value={
            location.timezone ? (
              <>
                {location.timezone}
                {clockOf(location) ? <span className="unit">{` · ${clockOf(location)} there`}</span> : null}
              </>
            ) : null
          }
        />
        <Row label="Continent" value={location.continent} />
        <Row
          label="European Union"
          value={location.country_code ? (location.in_european_union ? 'yes' : 'no') : null}
        />
        <Row
          label="Registered to"
          value={
            location.registered_country
              ? `${location.registered_country}${
                  location.registered_country_code ? ` (${location.registered_country_code})` : ''
                }`
              : null
          }
        />
      </Rows>
    </Section>
  );
}

function Claim({ source, differs }: { source: Source; differs: boolean }) {
  const place = placeOf(source.location);
  return (
    <div className="claim">
      <span className={differs ? 'claim-source differs' : 'claim-source'}>{source.source}</span>
      <span>
        <span className="claim-value">{place || 'no location'}</span>
        {coordsOf(source.location) ? (
          <span className="claim-meta">
            {coordsOf(source.location)}
            {source.location.accuracy_radius_km !== null
              ? ` ±${source.location.accuracy_radius_km} km`
              : ''}
          </span>
        ) : null}
      </span>
    </div>
  );
}

function NetworkSection({ data }: { data: BeaconResponse }) {
  const { network, sources } = data;
  const orgs = [...new Set(sources.map((s) => s.network.asn_org).filter(Boolean))];
  const disagrees = orgs.length > 1;

  return (
    <Section title="Network">
      <Rows>
        <Row label="Autonomous system" value={network.asn !== null ? `AS${network.asn}` : null} />
        <Row
          label="Organisation"
          value={
            disagrees ? (
              <>
                {orgs.map((o, i) => (
                  <span key={o}>
                    {o}
                    {i < orgs.length - 1 ? <span className="unit"> / </span> : null}
                  </span>
                ))}
              </>
            ) : (
              network.asn_org
            )
          }
        />
      </Rows>
      {disagrees ? (
        <p className="note">
          The sources agree on the network but spell its operator differently. That is a naming
          difference, not a disagreement about where the traffic goes.
        </p>
      ) : null}
    </Section>
  );
}

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
      <Ja4Breakdown ja4={tls.ja4} />

      <Rows>
        <Row label="JA4" value={tls.ja4} />
        <Row label="JA4 (wire order)" value={tls.ja4_o} />
        <Row label="JA3" value={tls.ja3_hash} />
        <Row label="JA3 (sorted)" value={tls.ja3n_hash} />
      </Rows>

      <p className="note">
        {tls.ja4 === tls.ja4_o
          ? 'The sorted and wire-order forms match, so this client sends its extensions in a stable order.'
          : 'The sorted and wire-order forms differ, so this client shuffles its extension order on every connection. That is why the sorted form exists.'}
      </p>

      <Rows>
        <Row label="Offered" value={hello.version} />
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

      {tls.negotiated ? (
        <>
          <p className="note">What the two sides settled on:</p>
          <Rows>
            <Row label="Version" value={tls.negotiated.version} />
            <Row label="Cipher" value={tls.negotiated.cipher_suite} />
            <Row label="Key exchange" value={tls.negotiated.key_exchange} />
            <Row label="Protocol" value={tls.negotiated.alpn} />
            <Row label="Resumed" value={tls.negotiated.resumed ? 'yes' : 'no'} />
          </Rows>
        </>
      ) : null}
    </Section>
  );
}

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

  return (
    <Section
      title="HTTP/2"
      intro="The frames your client sent before asking for anything. Libraries and browsers differ here more than you might expect."
    >
      <AkamaiBreakdown akamai={h2.akamai} />
      <Rows>
        <Row label="Fingerprint" value={h2.akamai_hash} />
        <Row
          label="Window update"
          value={h2.window_update > 0 ? `${h2.window_update.toLocaleString()} bytes` : null}
          absent="none sent"
        />
        <Row
          label="Priority frames"
          value={h2.priorities.length > 0 ? String(h2.priorities.length) : null}
          absent="none sent"
        />
      </Rows>
    </Section>
  );
}
