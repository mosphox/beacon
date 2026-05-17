'use client';

import { usePathname } from 'next/navigation';
import { useEffect, useState } from 'react';

export default function IpView() {
  const pathname = usePathname();
  const [data, setData] = useState(null);
  const [status, setStatus] = useState('loading');
  const [copyCount, setCopyCount] = useState(0);
  const [toastVisible, setToastVisible] = useState(false);

  useEffect(() => {
    let active = true;
    const target = process.env.NEXT_PUBLIC_DATA_URL || pathname || '/';
    fetch(target, { headers: { Accept: 'application/json' }, cache: 'no-store' })
      .then((r) => {
        if (!r.ok) throw new Error(`status ${r.status}`);
        return r.json();
      })
      .then((d) => {
        if (!active) return;
        if (!d || !d.ip) {
          setStatus('error');
          return;
        }
        setData(d);
        setStatus('ready');
      })
      .catch(() => {
        if (active) setStatus('error');
      });
    return () => {
      active = false;
    };
  }, [pathname]);

  useEffect(() => {
    if (copyCount === 0) return;
    setToastVisible(true);
    const t = setTimeout(() => setToastVisible(false), 2200);
    return () => clearTimeout(t);
  }, [copyCount]);

  const ip = data?.ip ?? '';
  const interactive = status === 'ready' && ip !== '';

  function copyIP() {
    if (!interactive || !navigator.clipboard?.writeText) return;
    navigator.clipboard
      .writeText(ip)
      .then(() => setCopyCount((n) => n + 1))
      .catch(() => {});
  }

  function onIpKeyDown(e) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      copyIP();
    }
  }

  const isRoot = !pathname || pathname === '/';
  const label = isRoot ? 'Your IP Address' : 'IP Lookup';

  const city = data?.city;
  const country = data?.country;
  const countryCode = data?.['country-code'];
  const asn = data?.asn;

  const locationParts = [];
  if (city) {
    locationParts.push(
      <span className="city" key="city">
        {city}
      </span>
    );
  }
  if (country) {
    locationParts.push(
      <span className="country" key="country">
        {country}
        {countryCode ? (
          <>
            {' '}
            <span className="country-code">({countryCode})</span>
          </>
        ) : null}
      </span>
    );
  }
  const locationContent = locationParts.flatMap((el, i) =>
    i === 0 ? [el] : [', ', el]
  );

  const ipText = { loading: '· · ·', error: 'unavailable', ready: ip }[status];

  return (
    <>
      {toastVisible && (
        <div
          className="copied show"
          key={copyCount}
          role="status"
          aria-live="polite"
        >
          Copied to clipboard
        </div>
      )}
      <div className="container">
        <div className="label">{label}</div>
        <div
          className={interactive ? 'ip' : 'ip static'}
          onClick={interactive ? copyIP : undefined}
          onKeyDown={interactive ? onIpKeyDown : undefined}
          role={interactive ? 'button' : undefined}
          tabIndex={interactive ? 0 : undefined}
          aria-label={interactive ? `Copy IP address ${ip}` : undefined}
        >
          {ipText}
        </div>
        {status === 'ready' &&
          (locationContent.length > 0 ? (
            <div className="location">{locationContent}</div>
          ) : (
            <div className="location unknown">Location unknown</div>
          ))}
        {status === 'ready' && asn && <div className="asn">{asn}</div>}
        <div className="copy-hint">Click to copy</div>
      </div>
    </>
  );
}
