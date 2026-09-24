/**
 * Fixture data for local development, shaped exactly like the Go backend's v2 response.
 *
 * The interesting states are the ones that are awkward to reach against a real backend:
 * sources disagreeing, a private address with no location at all, an IPv6 caller. The
 * fixture picks one deterministically from the address so a given URL always looks the
 * same, and reloading `/` cycles through them.
 */

import type { BeaconResponse, Location, Network, Provides, Source } from './types';

const EMPTY_LOCATION: Location = {
  city: null,
  region: null,
  region_code: null,
  subdivisions: [],
  postal_code: null,
  country: null,
  country_code: null,
  continent: null,
  continent_code: null,
  in_european_union: false,
  registered_country: null,
  registered_country_code: null,
  latitude: null,
  longitude: null,
  accuracy_radius_km: null,
  timezone: null,
  local_time: null,
  metro_code: null,
};

function location(partial: Partial<Location>): Location {
  return { ...EMPTY_LOCATION, ...partial };
}

function localTime(timezone: string): string {
  // Render "now" in the fixture's zone so the value moves like the real one does.
  const parts = new Intl.DateTimeFormat('sv-SE', {
    timeZone: timezone,
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(new Date());
  return parts.replace(' ', 'T');
}

const TBILISI = (): Location =>
  location({
    city: 'Tbilisi',
    region: 'Tbilisi',
    region_code: 'TB',
    subdivisions: [{ name: 'Tbilisi', code: 'TB' }],
    country: 'Georgia',
    country_code: 'GE',
    continent: 'Asia',
    continent_code: 'AS',
    registered_country: 'Georgia',
    registered_country_code: 'GE',
    latitude: 41.7151,
    longitude: 44.8271,
    accuracy_radius_km: 20,
    timezone: 'Asia/Tbilisi',
    local_time: localTime('Asia/Tbilisi'),
  });

const MOUNTAIN_VIEW = (): Location =>
  location({
    city: 'Mountain View',
    region: 'California',
    region_code: 'CA',
    subdivisions: [{ name: 'California', code: 'CA' }],
    postal_code: '94035',
    country: 'United States',
    country_code: 'US',
    continent: 'North America',
    continent_code: 'NA',
    latitude: 37.386,
    longitude: -122.0838,
    accuracy_radius_km: 1000,
    timezone: 'America/Los_Angeles',
    local_time: localTime('America/Los_Angeles'),
    metro_code: 807,
  });

const PHOENIX = (): Location =>
  location({
    city: 'Phoenix',
    region: 'Arizona',
    region_code: 'AZ',
    subdivisions: [{ name: 'Arizona', code: 'AZ' }],
    country: 'United States',
    country_code: 'US',
    continent: 'North America',
    continent_code: 'NA',
    latitude: 33.4484,
    longitude: -112.074,
    accuracy_radius_km: 50,
    timezone: 'America/Phoenix',
    local_time: localTime('America/Phoenix'),
  });

const BRISBANE = (): Location =>
  location({
    city: 'Brisbane',
    region: 'Queensland',
    region_code: 'QLD',
    subdivisions: [{ name: 'Queensland', code: 'QLD' }],
    country: 'Australia',
    country_code: 'AU',
    continent: 'Oceania',
    continent_code: 'OC',
    latitude: -27.4679,
    longitude: 153.0281,
    accuracy_radius_km: 1000,
    timezone: 'Australia/Brisbane',
    local_time: localTime('Australia/Brisbane'),
  });

const NO_FLAGS = { anycast: false, anonymous_proxy: false, satellite_provider: false };

/**
 * What each source provides, as the backend declares it. The fixtures only ever fill
 * these fields for a source, so the page meets the same mix of precision it does in
 * production: a city from DB-IP, a bare registered-country code from RIPE, and codes with
 * a city from the geofeeds.
 */
const PROVIDES = {
  MaxMind: [
    'city',
    'region',
    'postal_code',
    'coordinates',
    'accuracy_radius_km',
    'timezone',
    'metro_code',
    'country',
    'continent',
    'in_european_union',
    'registered_country',
    'asn',
    'asn_org',
    'anycast',
    'anonymous_proxy',
    'satellite_provider',
  ],
  'DB-IP': [
    'city',
    'region',
    'coordinates',
    'country',
    'continent',
    'in_european_union',
    'asn',
    'asn_org',
  ],
  IPinfo: ['country', 'continent', 'asn', 'asn_org'],
  IPLocate: ['country', 'continent', 'asn', 'asn_org'],
  RIPE: ['registered_country'],
  Geofeeds: ['city', 'region', 'country'],
} satisfies Record<string, Provides[]>;

type SourceName = keyof typeof PROVIDES;

function net(asn: number, org: string): Network {
  return { asn, asn_org: org, asn_label: `AS${asn} (${org})` };
}

const NO_NETWORK: Network = { asn: null, asn_org: null, asn_label: null };

/** A source's answer, cut down to the fields that source provides. */
function source(
  name: SourceName,
  loc: Location,
  network: Network = NO_NETWORK,
  flags: Partial<typeof NO_FLAGS> = {},
): Source {
  const provides: Provides[] = PROVIDES[name];
  const has = (k: Provides) => provides.includes(k);
  // These give codes, not names: the page names them.
  const codesOnly = name === 'Geofeeds';
  return {
    source: name,
    provides,
    location: location({
      city: has('city') ? loc.city : null,
      region: has('region') && !codesOnly ? loc.region : null,
      region_code: has('region') ? loc.region_code : null,
      subdivisions: has('region')
        ? loc.subdivisions.map((s) => (codesOnly ? { ...s, name: null } : s))
        : [],
      postal_code: has('postal_code') ? loc.postal_code : null,
      country: has('country') && !codesOnly ? loc.country : null,
      country_code: has('country') ? loc.country_code : null,
      continent: has('continent') ? loc.continent : null,
      continent_code: has('continent') ? loc.continent_code : null,
      in_european_union: has('in_european_union') ? loc.in_european_union : false,
      registered_country:
        has('registered_country') && name !== 'RIPE' ? loc.registered_country : null,
      registered_country_code: has('registered_country') ? loc.registered_country_code : null,
      latitude: has('coordinates') ? loc.latitude : null,
      longitude: has('coordinates') ? loc.longitude : null,
      accuracy_radius_km: has('accuracy_radius_km') ? loc.accuracy_radius_km : null,
      timezone: has('timezone') ? loc.timezone : null,
      local_time: has('timezone') ? loc.local_time : null,
      metro_code: has('metro_code') ? loc.metro_code : null,
    }),
    network: {
      asn: has('asn') ? network.asn : null,
      asn_org: has('asn_org') ? network.asn_org : null,
      asn_label: has('asn') ? network.asn_label : null,
    },
    flags: { ...NO_FLAGS, ...flags },
  };
}

const TLS_BLOCK = {
  ja3:
    '771,4867-4866-4865-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,' +
    '0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0',
  ja3_hash: 'cd08e31494f9531f560d64c695473da9',
  ja3n: '771,4867-4866-4865-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-5-10-11-13-16-18-23-27-35-43-45-51-65281-17513,29-23-24,0',
  ja3n_hash: '579ccef312d18482fc42e2b822ca2430',
  ja4: 't13d1516h2_8daaf6152771_e5627efa2ab1',
  ja4_r:
    't13d1516h2_002f,0035,009c,009d,1301,1302,1303,c013,c014,c02b,c02c,c02f,c030,cca8,cca9_' +
    '0005,000a,000b,000d,0012,0017,001b,002b,002d,0033,4469,ff01_' +
    '0403,0804,0401,0503,0805,0501,0806,0601',
  ja4_o: 't13d1516h2_acb858a92679_18f69afefd3d',
  ja4_ro:
    't13d1516h2_1301,1302,1303,c02b,c02f,c02c,c030,cca9,cca8,c013,c014,009c,009d,002f,0035_' +
    '0000,0017,ff01,000a,000b,0023,0010,0005,000d,0012,0033,002d,002b,001b,4469_' +
    '0403,0804,0401,0503,0805,0501,0806,0601',
  client_hello: {
    version: 'TLS 1.3',
    cipher_suites: ['0x1301', '0x1302', '0x1303', '0xc02b', '0xc02f', '0xc02c', '0xc030'],
    extensions: ['0x0000', '0x0017', '0xff01', '0x000a', '0x000b', '0x0023', '0x0010'],
    supported_versions: ['0x0304', '0x0303'],
    supported_groups: ['0x001d', '0x0017', '0x0018'],
    point_formats: [0],
    signature_algorithms: ['0x0403', '0x0804', '0x0401', '0x0503'],
    alpn: ['h2', 'http/1.1'],
    server_name: 'beacon.example.com',
    grease: true,
    truncated: false,
  },
  negotiated: {
    version: 'TLS 1.3',
    cipher_suite: 'TLS_AES_128_GCM_SHA256',
    key_exchange: 'X25519MLKEM768',
    alpn: 'h2',
    resumed: false,
    ech_accepted: false,
  },
};

const HTTP2_BLOCK = {
  akamai: '1:65536;2:0;4:6291456;6:262144|15663105|0|m,a,s,p',
  akamai_hash: '52d84b11737d980aef856699f885ca86',
  settings: [
    { id: 1, value: 65536 },
    { id: 2, value: 0 },
    { id: 4, value: 6291456 },
    { id: 6, value: 262144 },
  ],
  window_update: 15663105,
  priorities: [],
  pseudo_header_order: ['m', 'a', 's', 'p'],
};

type Fixture = {
  ip: string;
  family: string;
  hostname: string | null;
  sources: Source[];
  /** Whether the sources agree on the location; the network half is computed. */
  agree: boolean;
};

const FIXTURES: Fixture[] = [
  {
    // Every source, agreeing: the country-level ones fold into the city, and the registry
    // joins only the country table.
    ip: '203.0.113.84',
    family: 'ipv4',
    hostname: '84.33.3.149.silknet.com',
    agree: true,
    sources: [
      source('MaxMind', TBILISI(), net(35805, 'SILKNET-AS')),
      source('DB-IP', TBILISI(), net(35805, 'JSC Silknet')),
      source('IPinfo', TBILISI(), net(35805, 'JSC Silknet')),
      source('IPLocate', TBILISI(), net(35805, 'JSC "Silknet"')),
      source('RIPE', TBILISI()),
      source('Geofeeds', TBILISI()),
    ],
  },
  {
    // Sources disagreeing — beacon's differentiator, and awkward to reproduce on demand.
    // IPinfo spells the country its own way here, which must not read as a disagreement.
    ip: '8.8.8.8',
    family: 'ipv4',
    hostname: 'dns.google',
    agree: false,
    sources: [
      source('MaxMind', MOUNTAIN_VIEW(), net(15169, 'GOOGLE'), { anycast: true }),
      source('DB-IP', PHOENIX(), net(15169, 'Google LLC')),
      source('IPLocate', MOUNTAIN_VIEW(), net(15169, 'Google LLC')),
      source(
        'IPinfo',
        { ...MOUNTAIN_VIEW(), country: 'United States of America' },
        net(15169, 'Google LLC'),
      ),
    ],
  },
  {
    // One source alone: no comparison, and nothing that checks for the flags.
    ip: '198.51.100.23',
    family: 'ipv4',
    hostname: null,
    agree: true,
    sources: [source('DB-IP', PHOENIX(), net(64496, 'Example Transit'))],
  },
  {
    // No location at all: the empty state, which must read as an answer.
    ip: '10.24.0.7',
    family: 'ipv4',
    hostname: null,
    agree: true,
    sources: [],
  },
  {
    ip: '2606:4700:4700::1111',
    family: 'ipv6',
    hostname: 'one.one.one.one',
    // A country-level dissent from a source that names no country: the page names it.
    agree: false,
    sources: [
      source('MaxMind', BRISBANE(), net(13335, 'CLOUDFLARENET'), { anycast: true }),
      source('IPLocate', BRISBANE(), net(13335, 'Cloudflare, Inc.')),
      source('Geofeeds', location({ country_code: 'US' })),
    ],
  },
];

function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/**
 * build returns a fixture for a path. A specific address is echoed back so `/1.2.3.4`
 * shows that address; the root cycles so every state is reachable by reloading.
 */
export function build(requested: string | null, spin: number): BeaconResponse {
  const base =
    requested !== null
      ? (FIXTURES.find((f) => f.ip === requested) ?? FIXTURES[hash(requested) % FIXTURES.length])
      : FIXTURES[spin % FIXTURES.length];

  const ip = requested ?? base.ip;
  const primary = base.sources[0];
  // A fixture's `agree` is about location; the network half follows from the numbers.
  const networksAgree =
    new Set(base.sources.map((s) => s.network.asn).filter((a) => a !== null)).size <= 1;

  return {
    version: 2,
    ip,
    family: requested !== null && requested.includes(':') ? 'ipv6' : base.family,
    hostname: requested !== null && requested !== base.ip ? null : base.hostname,
    location: primary ? primary.location : EMPTY_LOCATION,
    network: primary ? primary.network : { asn: null, asn_org: null, asn_label: null },
    flags: primary ? primary.flags : NO_FLAGS,
    sources_agree: base.agree && networksAgree,
    locations_agree: base.agree,
    networks_agree: networksAgree,
    sources: base.sources,
    tls: TLS_BLOCK,
    http2: HTTP2_BLOCK,
  };
}
