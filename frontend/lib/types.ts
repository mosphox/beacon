/**
 * The v2 response, mirroring `backend/internal/render/render.go`.
 *
 * Nearly everything is nullable, and that is information rather than noise: a null city
 * means no source had one for this address, which is a different statement from an empty
 * string. Keep the nulls in the types so the UI is forced to decide what to say.
 */

export type Subdivision = {
  /** Null when a source gives only the code, as a geofeed does. */
  name: string | null;
  code: string | null;
};

export type Location = {
  city: string | null;
  region: string | null;
  region_code: string | null;
  subdivisions: Subdivision[];
  postal_code: string | null;
  country: string | null;
  country_code: string | null;
  continent: string | null;
  continent_code: string | null;
  in_european_union: boolean;
  registered_country: string | null;
  registered_country_code: string | null;
  latitude: number | null;
  longitude: number | null;
  accuracy_radius_km: number | null;
  timezone: string | null;
  local_time: string | null;
  metro_code: number | null;
};

export type Network = {
  asn: number | null;
  asn_org: string | null;
  asn_label: string | null;
};

export type Flags = {
  anycast: boolean;
  anonymous_proxy: boolean;
  satellite_provider: boolean;
};

/**
 * A field a source can fill at all, named by the response key it governs: `region` covers
 * the region and subdivisions, `coordinates` latitude and longitude, `timezone` the local
 * time derived from it.
 *
 * A null — or a false flag — from a source that provides the field is that source's
 * answer. From one that does not, it says nothing either way: DB-IP Lite has no postal
 * code for any address, and a country-only database has no city to give.
 */
export type Provides =
  | 'city'
  | 'region'
  | 'postal_code'
  | 'coordinates'
  | 'accuracy_radius_km'
  | 'timezone'
  | 'metro_code'
  | 'country'
  | 'continent'
  | 'in_european_union'
  | 'registered_country'
  | 'asn'
  | 'asn_org'
  | 'anycast'
  | 'anonymous_proxy'
  | 'satellite_provider';

export type Source = {
  source: string;
  provides: Provides[];
  location: Location;
  network: Network;
  flags: Flags;
};

export type ClientHello = {
  version: string;
  cipher_suites: string[];
  extensions: string[];
  supported_versions: string[];
  supported_groups: string[];
  point_formats: number[];
  signature_algorithms: string[];
  alpn: string[];
  server_name: string | null;
  grease: boolean;
  truncated: boolean;
};

export type NegotiatedTls = {
  version: string;
  cipher_suite: string;
  key_exchange: string | null;
  alpn: string | null;
  resumed: boolean;
  ech_accepted: boolean;
};

export type TlsBlock = {
  ja3: string;
  ja3_hash: string;
  ja3n: string;
  ja3n_hash: string;
  ja4: string;
  ja4_r: string;
  ja4_o: string;
  ja4_ro: string;
  client_hello: ClientHello;
  negotiated: NegotiatedTls | null;
};

export type Http2Setting = {
  id: number;
  value: number;
};

export type Http2Priority = {
  stream_id: number;
  exclusive: boolean;
  depends_on: number;
  weight: number;
};

export type Http2Block = {
  akamai: string;
  akamai_hash: string;
  settings: Http2Setting[];
  window_update: number;
  priorities: Http2Priority[];
  pseudo_header_order: string[];
};

export type BeaconResponse = {
  version: number;
  ip: string;
  family: string | null;
  hostname: string | null;
  location: Location;
  network: Network;
  flags: Flags;
  sources_agree: boolean;
  /**
   * The two halves of `sources_agree`. Sources routinely agree on where an address is
   * while naming different autonomous systems for it — routing data against registry
   * data — so the Location section must not report the network half as its own.
   */
  locations_agree: boolean;
  networks_agree: boolean;
  sources: Source[];
  /** Null unless beacon terminated this connection's TLS itself. */
  tls: TlsBlock | null;
  /** Null unless the request arrived over HTTP/2. */
  http2: Http2Block | null;
};

/** HTTP/2 setting names, for rendering ids as something readable (RFC 9113 §6.5.2). */
export const SETTING_NAMES: Record<number, string> = {
  1: 'HEADER_TABLE_SIZE',
  2: 'ENABLE_PUSH',
  3: 'MAX_CONCURRENT_STREAMS',
  4: 'INITIAL_WINDOW_SIZE',
  5: 'MAX_FRAME_SIZE',
  6: 'MAX_HEADER_LIST_SIZE',
  8: 'ENABLE_CONNECT_PROTOCOL',
  9: 'NO_RFC7540_PRIORITIES',
};

/** The pseudo-headers a JA4-style order string abbreviates. */
export const PSEUDO_HEADER_NAMES: Record<string, string> = {
  m: ':method',
  s: ':scheme',
  a: ':authority',
  p: ':path',
};
