'use client';

/**
 * The boundary of last resort: a throw in the root layout itself, which
 * `app/error.tsx` cannot catch because it renders inside that layout. It has to
 * supply its own <html> and <body>, and it cannot rely on the fonts or the
 * stylesheet having loaded, so everything here is inline.
 */
export default function GlobalError({ reset }: { error: Error; reset: () => void }) {
  return (
    <html lang="en">
      <body
        style={{
          margin: 0,
          minHeight: '100vh',
          display: 'grid',
          placeItems: 'center',
          background: '#0b0b1a',
          color: '#e0e0e0',
          fontFamily: 'system-ui, sans-serif',
          textAlign: 'center',
          padding: '24px',
        }}
      >
        <div>
          <h1 style={{ fontSize: '1.25rem', fontWeight: 600 }}>Beacon could not start.</h1>
          <p style={{ color: '#9ca3af', marginTop: '8px' }}>Reload to try again.</p>
          <button
            type="button"
            onClick={reset}
            style={{
              marginTop: '24px',
              padding: '8px 16px',
              borderRadius: '4px',
              border: '1px solid rgb(255 255 255 / 0.12)',
              background: 'none',
              color: 'inherit',
              font: 'inherit',
              cursor: 'pointer',
            }}
          >
            Reload
          </button>
        </div>
      </body>
    </html>
  );
}
