import { PSEUDO_HEADER_NAMES, SETTING_NAMES } from '@/lib/types';

/**
 * A JA4 string is three parts joined by underscores, and the first is itself packed:
 * protocol, TLS version, whether SNI was sent, how many ciphers and extensions, and the
 * ALPN. Every other service shows this as one undifferentiated blob. Taking it apart is
 * the point of the page — the server computed it and knows what each field means.
 */
function decodePrefix(prefix: string): { code: string; meaning: string }[] {
  if (prefix.length < 10) return [];

  const protocol = prefix[0];
  const version = prefix.slice(1, 3);
  const sni = prefix[3];
  const ciphers = prefix.slice(4, 6);
  const extensions = prefix.slice(6, 8);
  const alpn = prefix.slice(8, 10);

  const versions: Record<string, string> = {
    '13': 'TLS 1.3',
    '12': 'TLS 1.2',
    '11': 'TLS 1.1',
    '10': 'TLS 1.0',
    s3: 'SSL 3.0',
    s2: 'SSL 2.0',
    '00': 'unknown version',
  };

  return [
    {
      code: protocol,
      meaning: protocol === 'q' ? 'over QUIC' : 'over TCP',
    },
    {
      code: version,
      meaning: versions[version] ?? `version ${version}`,
    },
    {
      code: sni,
      meaning:
        sni === 'd'
          ? 'connected to a domain name, so SNI was sent'
          : 'connected to an address, so no SNI',
    },
    {
      code: ciphers,
      meaning: `${Number(ciphers)} cipher suites offered`,
    },
    {
      code: extensions,
      meaning: `${Number(extensions)} extensions offered`,
    },
    {
      code: alpn,
      meaning: alpn === '00' ? 'no ALPN offered' : `first ALPN starts and ends "${alpn}"`,
    },
  ];
}

export function Ja4Breakdown({ ja4 }: { ja4: string }) {
  const [prefix, ciphers, extensions] = ja4.split('_');
  const parts = decodePrefix(prefix ?? '');

  return (
    <div className="fp">
      <p className="fp-name">JA4</p>
      <p className="fp-string">
        <span className="fp-seg prefix">{prefix}</span>
        <span className="fp-sep">_</span>
        <span className="fp-seg">{ciphers}</span>
        <span className="fp-sep">_</span>
        <span className="fp-seg">{extensions}</span>
      </p>
      <dl className="fp-parts">
        {parts.map((p) => (
          <div key={p.code + p.meaning} style={{ display: 'contents' }}>
            <dt>{p.code}</dt>
            <dd>{p.meaning}</dd>
          </div>
        ))}
        <div style={{ display: 'contents' }}>
          <dt>{ciphers}</dt>
          <dd>the cipher suites, sorted and hashed</dd>
        </div>
        <div style={{ display: 'contents' }}>
          <dt>{extensions}</dt>
          <dd>the extensions and signature algorithms, sorted and hashed</dd>
        </div>
      </dl>
    </div>
  );
}

/**
 * The Akamai HTTP/2 fingerprint is four fields: the client's SETTINGS, its first
 * connection window update, any PRIORITY frames, and the order it wrote its
 * pseudo-headers in. Shown apart, each one is readable.
 */
export function AkamaiBreakdown({ akamai }: { akamai: string }) {
  const [settings, windowUpdate, priority, pseudo] = akamai.split('|');

  const settingList = (settings ?? '')
    .split(';')
    .filter(Boolean)
    .map((pair) => {
      const [id, value] = pair.split(':');
      const name = SETTING_NAMES[Number(id)];
      return name ? `${name} = ${value}` : `setting ${id} = ${value}`;
    });

  const headerList = (pseudo ?? '')
    .split(',')
    .filter(Boolean)
    .map((c) => PSEUDO_HEADER_NAMES[c] ?? `:${c}`);

  return (
    <div className="fp">
      <p className="fp-name">Akamai HTTP/2</p>
      <p className="fp-string">
        <span className="fp-seg prefix">{settings || '—'}</span>
        <span className="fp-sep">|</span>
        <span className="fp-seg">{windowUpdate}</span>
        <span className="fp-sep">|</span>
        <span className="fp-seg">{priority}</span>
        <span className="fp-sep">|</span>
        <span className="fp-seg">{pseudo}</span>
      </p>
      <dl className="fp-parts">
        <div style={{ display: 'contents' }}>
          <dt>{settings || '—'}</dt>
          <dd>{settingList.length > 0 ? settingList.join(', ') : 'no settings sent'}</dd>
        </div>
        <div style={{ display: 'contents' }}>
          <dt>{windowUpdate}</dt>
          <dd>
            {windowUpdate === '00'
              ? 'no connection window update before the first request'
              : `connection window raised by ${Number(windowUpdate).toLocaleString()} bytes`}
          </dd>
        </div>
        <div style={{ display: 'contents' }}>
          <dt>{priority}</dt>
          <dd>{priority === '0' ? 'no PRIORITY frames' : 'PRIORITY frames, as stream:exclusive:parent:weight'}</dd>
        </div>
        <div style={{ display: 'contents' }}>
          <dt>{pseudo}</dt>
          <dd>{headerList.join(' then ')}</dd>
        </div>
      </dl>
    </div>
  );
}
