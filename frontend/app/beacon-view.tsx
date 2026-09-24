'use client';

import { animate, useReducedMotion } from 'motion/react';
import { usePathname } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';

import { AkamaiBreakdown, Ja4Breakdown } from './components/fingerprint';
import type { Ledger, LedgerRow } from './components/ledgers';
import { Ledgers } from './components/ledgers';
import { Chips, Flag, Group, Row, Rows, Section } from './components/rows';
import type { BeaconResponse, Location, Network, Provides, Source } from '@/lib/types';
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
              <SourcesSection data={data} />
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
 * GeoLite2 EULA requires its notice verbatim. DB-IP Lite is CC BY 4.0 and
 * IPinfo's and IPLocate's data CC BY-SA 4.0, all of which require attribution on
 * the output and not only in the README; IPinfo and IPLocate ask for a link in so
 * many words. The RIPE Database's terms ask for no notice and geofeeds carry no
 * licence, but a source this page shows by name is a source it credits.
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
        <a href="https://creativecommons.org/licenses/by/4.0/">CC&nbsp;BY&nbsp;4.0</a>. IP address
        data powered by <a href="https://ipinfo.io">IPinfo</a> and{' '}
        <a href="https://www.iplocate.io">IPLocate.io</a>, both licensed under{' '}
        <a href="https://creativecommons.org/licenses/by-sa/4.0/">CC&nbsp;BY-SA&nbsp;4.0</a>.
        Registration data from the{' '}
        <a href="https://www.ripe.net/manage-ips-and-asns/db/">RIPE&nbsp;Database</a>, under
        its <a href="https://docs.db.ripe.net/HTML-Terms-And-Conditions">terms and conditions</a>,
        and locations the networks themselves publish as{' '}
        <a href="https://www.rfc-editor.org/rfc/rfc8805">geofeeds</a>, found through it.
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

const FLAGS: { key: 'anycast' | 'anonymous_proxy' | 'satellite_provider'; label: string }[] = [
  { key: 'anycast', label: 'anycast' },
  { key: 'anonymous_proxy', label: 'anonymous proxy' },
  { key: 'satellite_provider', label: 'satellite' },
];

function Connection({ data }: { data: BeaconResponse }) {
  const { flags, sources } = data;
  const marks = FLAGS.filter((f) => flags[f.key]).map((f) => f.label);
  // A mark is one source's assertion, so it carries that source's name. And "nothing
  // unusual" is only an answer when some source here looks for these at all: DB-IP Lite
  // never does, and its false is not a clean bill.
  const markedBy = sources.filter((s) => FLAGS.some((f) => s.flags[f.key])).map((s) => s.source);
  const checked =
    sources.length === 0 || sources.some((s) => FLAGS.some((f) => s.provides.includes(f.key)));

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
            absent={checked ? 'nothing unusual' : 'no source here checks this'}
            value={
              marks.length > 0 ? (
                <>
                  {marks.map((m) => (
                    <Flag key={m} on>
                      {m}
                    </Flag>
                  ))}{' '}
                  <span className="marked-by">
                    per <span translate="no">{listOf(markedBy)}</span>
                  </span>
                </>
              ) : null
            }
          />
        </Rows>
      </Group>
    </Section>
  );
}

/* ----------------------------------------------------- network and location */

/**
 * One line of a source's ledger.
 *
 * `needs` is what a source must provide for the field to be in its ledger at all. Sources
 * differ in precision — some place an address in a city, some only in a country — and a
 * field outside a source's data is left out of that source's ledger rather than shown
 * empty, and does not count when deciding whether the sources differ on it.
 */
type LedgerField = {
  key: string;
  label: string;
  needs: Provides;
  topic: 'network' | 'place' | 'registration';
  of: (l: Location, n: Network) => string | null;
  /** Left alone by a page translator: an identifier or a name, not a word. */
  literal?: boolean;
};

/**
 * Every ledger's fields, in one order: the network, then the place from the continent
 * down, then the registration.
 *
 * The order is what lines the ledgers up. Each database's fields run unbroken from the top
 * of the list: IPinfo and IPLocate cover the first four, DB-IP those and the next four,
 * MaxMind all of them. So a field falls on the same line in every database's ledger with
 * no gap left for a field a source does not carry — and that holds only while the network
 * comes first, because it is the one thing every database gives. Putting the place first
 * leaves IPinfo's operator nine lines above MaxMind's. The geofeeds and the registry cover
 * too little to line up with anything and are listed in the same order from the top.
 */
const LEDGER_FIELDS: LedgerField[] = [
  {
    key: 'asn',
    label: 'Autonomous system',
    needs: 'asn',
    topic: 'network',
    of: (_, n) => (n.asn === null ? null : `AS${n.asn}`),
    literal: true,
  },
  {
    key: 'org',
    label: 'Operator',
    needs: 'asn_org',
    topic: 'network',
    of: (_, n) => n.asn_org,
    literal: true,
  },
  {
    key: 'continent',
    label: 'Continent',
    needs: 'continent',
    topic: 'place',
    of: (l) => l.continent,
  },
  {
    // The name alone: it is one name per code by now (see withPlaceNames).
    key: 'country',
    label: 'Country',
    needs: 'country',
    topic: 'place',
    of: (l) => l.country ?? l.country_code,
  },
  {
    key: 'eu',
    label: 'European Union',
    needs: 'in_european_union',
    topic: 'place',
    of: (l) => (l.country_code ? (l.in_european_union ? 'yes' : 'no') : null),
  },
  {
    // A geofeed gives the region as a code. It is named where another source names the same
    // code (see withPlaceNames); otherwise the code is the geofeed's answer, shown as one.
    key: 'region',
    label: 'Region',
    needs: 'region',
    topic: 'place',
    of: (l) => l.region ?? l.region_code,
  },
  { key: 'city', label: 'City', needs: 'city', topic: 'place', of: (l) => l.city },
  { key: 'coords', label: 'Coordinates', needs: 'coordinates', topic: 'place', of: coordsOf },
  {
    key: 'accuracy',
    label: 'Accuracy',
    needs: 'accuracy_radius_km',
    topic: 'place',
    of: (l) => (l.accuracy_radius_km === null ? null : `±${l.accuracy_radius_km}\u00A0km`),
  },
  {
    key: 'postal',
    label: 'Postal code',
    needs: 'postal_code',
    topic: 'place',
    of: (l) => l.postal_code,
  },
  {
    key: 'tz',
    label: 'Time zone',
    needs: 'timezone',
    topic: 'place',
    of: (l) => l.timezone,
    literal: true,
  },
  {
    key: 'clock',
    label: 'Local time',
    needs: 'timezone',
    topic: 'place',
    of: (l) => (clockOf(l) ? `${clockOf(l)} there` : null),
  },
  {
    // The name alone, as for Country. RIPE gives only the code; withPlaceNames names it.
    key: 'registered',
    label: 'Registered to',
    needs: 'registered_country',
    topic: 'registration',
    of: (l) => l.registered_country ?? l.registered_country_code,
  },
];

/**
 * What places an address. A registry places nothing: RIPE names the country an address is
 * registered to, which for a VPN or a leased range is nowhere near the visitor, so it is
 * not counted in "N sources agree on the location".
 */
const PLACING: Provides[] = [
  'city',
  'region',
  'postal_code',
  'coordinates',
  'accuracy_radius_km',
  'timezone',
  'country',
];

const places = (s: Source) => PLACING.some((k) => s.provides.includes(k));

/** What each source is, said beside its name. */
const SOURCE_KIND: Record<string, string> = {
  MaxMind: 'location database',
  'DB-IP': 'location database',
  IPinfo: 'location database',
  IPLocate: 'location database',
  Geofeeds: 'the operator’s own geofeed',
  RIPE: 'registry',
};

const LIST = new Intl.ListFormat('en', { style: 'long', type: 'conjunction' });

function listOf(names: string[]): string {
  return LIST.format(names);
}

const REGIONS = new Intl.DisplayNames(['en'], { type: 'region' });

/**
 * Every location with its countries named the same way for the same code: the first
 * source's spelling, or the browser's where no source names it. That goes for the
 * registered country too, which is the same list of countries.
 *
 * Databases can spell a country differently — "United States of America" against "United
 * States" — and RIPE and the geofeeds give only the code. Compared as text, a spelling
 * would read as a disagreement about where you are. The response keeps
 * each source's own spelling; this is only how they are set side by side.
 *
 * A region given by code alone, as geofeeds give it, takes the name a source uses for the
 * same code in the same country. The browser has no names for regions, so where no source
 * names it, it goes unnamed.
 */
function withPlaceNames(sources: Source[]): Location[] {
  const names = new Map<string, string>();
  const learn = (code: string | null, name: string | null) => {
    if (code && name && !names.has(code)) names.set(code, name);
  };
  for (const { location: l } of sources) learn(l.country_code, l.country);
  for (const { location: l } of sources) learn(l.registered_country_code, l.registered_country);

  const regions = new Map<string, string>();
  const regionKey = (l: Location) =>
    l.country_code && l.region_code ? `${l.country_code}-${l.region_code}` : null;
  for (const { location: l } of sources) {
    const key = regionKey(l);
    if (key && l.region && !regions.has(key)) regions.set(key, l.region);
  }

  const nameOf = (code: string) => names.get(code) ?? regionName(code);
  return sources.map(({ location: l }) => {
    const country = l.country_code ? nameOf(l.country_code) : l.country;
    const registered = l.registered_country_code
      ? nameOf(l.registered_country_code)
      : l.registered_country;
    const key = regionKey(l);
    const region = l.region ?? (key ? (regions.get(key) ?? null) : null);
    return country === l.country && registered === l.registered_country && region === l.region
      ? l
      : { ...l, country, registered_country: registered, region };
  });
}

function regionName(code: string): string | null {
  try {
    return REGIONS.of(code) ?? null;
  } catch {
    // Not a region code the browser knows; the code stands on its own.
    return null;
  }
}


/** Letters that carry their accent in their shape, which NFD does not take apart. */
const PLAIN: Record<string, string> = {
  ø: 'o',
  ł: 'l',
  đ: 'd',
  ß: 'ss',
  æ: 'ae',
  œ: 'oe',
  ı: 'i',
  þ: 'th',
};

/**
 * A value as the comparison sees it: case and accents aside, so "Malmö" is "Malmo". A
 * geofeed is often written in plain ASCII, and a spelling is not a disagreement.
 */
function fold(v: string | null): string | null {
  if (v === null) return null;
  return v
    .normalize('NFD')
    .replace(/\p{Mn}/gu, '')
    .toLowerCase()
    .replace(/[øłđßæœıþ]/g, (c) => PLAIN[c]);
}

/**
 * Every source's ledger, each row marked where the sources that cover its field answer it
 * differently.
 *
 * Only a source that covers a field is compared on it. One that never carries the field —
 * DB-IP Lite has no postal codes, a country-only database no coordinates — has not
 * disagreed by having nothing there. One that carries it and has nothing for this address
 * has answered, and "not available" against a city is a difference.
 */
function ledgersOf(sources: Source[]): Ledger[] {
  const named = withPlaceNames(sources);
  // Per field, per source: the answer, or undefined where the source does not cover the field.
  const answers = LEDGER_FIELDS.map((f) =>
    sources.map((s, i) => (s.provides.includes(f.needs) ? f.of(named[i], s.network) : undefined)),
  );
  const differs = answers.map((given) => {
    const compared = given.filter((a) => a !== undefined).map(fold);
    return compared.some((v) => v !== compared[0]);
  });

  return sources.flatMap((s, i) => {
    let topic: LedgerField['topic'] | null = null;
    const rows = LEDGER_FIELDS.flatMap((f, fi): LedgerRow[] => {
      const value = answers[fi][i];
      if (value === undefined) return [];
      const opens = topic !== null && topic !== f.topic;
      topic = f.topic;
      return [
        {
          key: f.key,
          label: f.label,
          value,
          differs: differs[fi],
          opens,
          literal: f.literal ?? false,
        },
      ];
    });
    return rows.length > 0 ? [{ source: s.source, kind: SOURCE_KIND[s.source] ?? null, rows }] : [];
  });
}

function SourcesSection({ data }: { data: BeaconResponse }) {
  const title = 'Network and location';

  if (data.sources.length === 0) {
    return (
      <Section title={title}>
        <p className="note">
          No database has a network or a location for this address. Private and reserved ranges
          belong to no autonomous system and are not geolocated.
        </p>
      </Section>
    );
  }

  // The registry last: its one line is the registration every other ledger ends on.
  // Otherwise the order the backend gives, which is its order of preference.
  const sources = [...data.sources].sort((a, b) => Number(!places(a)) - Number(!places(b)));
  const placing = sources.filter(places);
  const multiple = placing.length > 1;
  const agree = data.locations_agree;
  const withNetwork = sources.filter((s) => s.provides.includes('asn'));
  const orgs = new Set(withNetwork.map((s) => s.network.asn_org).filter(Boolean));
  const asns = new Set(withNetwork.map((s) => s.network.asn).filter((a) => a !== null));

  return (
    <Section
      title={title}
      note={
        multiple
          ? `${placing.length} sources ${agree ? 'agree' : 'disagree'} on the location`
          : undefined
      }
      noteTone={multiple ? (agree ? 'agree' : 'differ') : undefined}
      intro={
        multiple && !agree
          ? 'The databases place this address differently. Each answer stands as reported; none is corrected against another, and the fields they differ on are marked.'
          : undefined
      }
    >
      <Ledgers ledgers={ledgersOf(sources)} />
      {orgs.size > 1 && asns.size === 1 ? (
        <p className="group-note">
          The sources agree on the network but spell its operator differently. That is a naming
          difference, not a disagreement about where the traffic goes.
        </p>
      ) : null}
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
