'use client';

import { useEffect } from 'react';

/**
 * The boundary for anything that throws while rendering.
 *
 * Without one, a single bad field takes the whole page: a null where an array
 * was promised, or a v1-shaped body arriving from a backend that was restarted
 * before this image caught up, and the visitor gets Next's generic crash screen
 * instead of the one thing they came for. This keeps the answer on screen and
 * says what is missing.
 */
export default function Error({ error, reset }: { error: Error; reset: () => void }) {
  useEffect(() => {
    // Server-side this is already logged; in the browser it is otherwise lost.
    console.error(error);
  }, [error]);

  return (
    <main className="state">
      <div>
        <h1>Something in the response could not be read.</h1>
        <p className="state-detail">
          The lookup service answered, but not in a shape this page understands. That
          usually means the two are running different versions for a moment.
        </p>
        <button type="button" className="retry" onClick={reset}>
          Try again
        </button>
      </div>
    </main>
  );
}
