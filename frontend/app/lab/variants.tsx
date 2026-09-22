'use client';

import { useEffect, useRef, useState } from 'react';

const ADDRESS = '149.3.33.84';

/** Matches what ships: the brackets bite in for 75ms, then the hover state resumes. */
const SNAP_HOLD = 75;

/**
 * Nothing here writes to the clipboard. These are motion studies and a lab page should
 * not quietly replace what is on someone's clipboard while they browse it. The toast is
 * the real one from globals.css, so the glass is judged against the real background.
 */
function useFire() {
  const [snap, setSnap] = useState(false);
  const [note, setNote] = useState(0);
  const snapTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const noteTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (snapTimer.current) clearTimeout(snapTimer.current);
      if (noteTimer.current) clearTimeout(noteTimer.current);
    },
    [],
  );

  function fire() {
    setSnap(true);
    setNote((n) => n + 1);
    if (snapTimer.current) clearTimeout(snapTimer.current);
    if (noteTimer.current) clearTimeout(noteTimer.current);
    snapTimer.current = setTimeout(() => setSnap(false), SNAP_HOLD);
    noteTimer.current = setTimeout(() => setNote(0), 2200);
  }

  return { snap, note, fire };
}

/* ------------------------------------------------------------------ the address */

/**
 * The address is a gradient clipped to the glyphs over a blurred duplicate that glows.
 *
 * `.demo-text` itself can never be transformed, masked or given a per-child opacity —
 * that puts it in its own painting context and the clipped background stops
 * compositing. An ANCESTOR is fine, which is what every transform below moves, and so
 * are paint and layout properties of the text such as letter-spacing.
 *
 * The glow is flat colour with no clipped background, so it may be split per character
 * and moved freely. That is how the last variant works.
 */
function Address({ splitGlow = false }: { splitGlow?: boolean }) {
  return (
    <>
      <span className="ip-bracket left" aria-hidden="true">
        [
      </span>
      <span className="demo-stack">
        <span className="demo-glow" aria-hidden="true">
          {splitGlow
            ? [...ADDRESS].map((ch, i) => (
                <span className="glow-ch" key={`${i}${ch}`}>
                  {ch}
                </span>
              ))
            : ADDRESS}
        </span>
        <span className="demo-text" aria-hidden="true">
          {ADDRESS}
        </span>
      </span>
      <span className="ip-bracket right" aria-hidden="true">
        ]
      </span>
      <span className="sr-only">{ADDRESS}</span>
    </>
  );
}

type Run = (btn: HTMLButtonElement) => void;

/**
 * Every variant is the same button and the same bracket snap. Only `run` differs, so
 * what you are comparing is the address's movement and nothing else.
 */
function Demo({ run, className, splitGlow }: { run: Run; className?: string; splitGlow?: boolean }) {
  const ref = useRef<HTMLButtonElement>(null);
  const { snap, note, fire } = useFire();

  function onClick() {
    fire();
    if (ref.current && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
      run(ref.current);
    }
  }

  return (
    <>
      <button
        ref={ref}
        type="button"
        className={`demo-btn c-snap ${snap ? 'go' : ''} ${className ?? ''}`}
        onClick={onClick}
      >
        <Address splitGlow={splitGlow} />
      </button>
      {note > 0 ? (
        <output className="copied" key={note} aria-live="polite">
          Copied to clipboard
        </output>
      ) : null}
    </>
  );
}

/* --------------------------------------------------------------------- scaling */

/** Down hard, past the resting size on the way back, then settles. */
export function Punch() {
  return (
    <Demo
      run={(b) =>
        b.animate(
          [
            { transform: 'scale(1)' },
            { transform: 'scale(0.93)', offset: 0.2 },
            { transform: 'scale(1.025)', offset: 0.58 },
            { transform: 'scale(1)' },
          ],
          { duration: 340, easing: 'ease-out' },
        )
      }
    />
  );
}

/** The other direction: the value jumps toward you instead of away. */
export function Pop() {
  return (
    <Demo
      run={(b) =>
        b.animate([{ transform: 'scale(1)' }, { transform: 'scale(1.05)', offset: 0.3 }, { transform: 'scale(1)' }], {
          duration: 280,
          easing: 'cubic-bezier(0.2, 0, 0, 1)',
        })
      }
    />
  );
}

/** Wider and flatter, then the reverse — the cartoonist's squash and stretch. */
export function Squash() {
  return (
    <Demo
      run={(b) =>
        b.animate(
          [
            { transform: 'scale(1, 1)' },
            { transform: 'scale(1.035, 0.92)', offset: 0.22 },
            { transform: 'scale(0.985, 1.03)', offset: 0.55 },
            { transform: 'scale(1, 1)' },
          ],
          { duration: 360, easing: 'ease-out' },
        )
      }
    />
  );
}

/** No travel inward at all: it is already small when you look, and it grows back. */
export function Settle() {
  return (
    <Demo
      run={(b) =>
        b.animate([{ transform: 'scale(0.95)' }, { transform: 'scale(1)' }], {
          duration: 420,
          easing: 'cubic-bezier(0.16, 1, 0.3, 1)',
        })
      }
    />
  );
}

/* -------------------------------------------------------------------- movement */

/** Straight down and back, the way a key travels under a finger. */
export function Keypress() {
  return (
    <Demo
      run={(b) =>
        b.animate(
          [
            { transform: 'translateY(0)' },
            { transform: 'translateY(6px)', offset: 0.22 },
            { transform: 'translateY(0)' },
          ],
          { duration: 260, easing: 'cubic-bezier(0.3, 0, 0, 1)' },
        )
      }
    />
  );
}

/** Pushed into the screen under perspective, so it foreshortens rather than just shrinking. */
export function Depth() {
  return (
    <Demo
      run={(b) =>
        b.animate(
          [
            { transform: 'perspective(900px) translateZ(0)' },
            { transform: 'perspective(900px) translateZ(-90px)', offset: 0.28 },
            { transform: 'perspective(900px) translateZ(0)' },
          ],
          { duration: 340, easing: 'ease-out' },
        )
      }
    />
  );
}

/** The top edge tips away from you, as though the whole line hinged at its base. */
export function Tilt() {
  return (
    <Demo
      run={(b) =>
        b.animate(
          [
            { transform: 'perspective(900px) rotateX(0deg)' },
            { transform: 'perspective(900px) rotateX(14deg)', offset: 0.28 },
            { transform: 'perspective(900px) rotateX(0deg)' },
          ],
          { duration: 360, easing: 'ease-out' },
        )
      }
    />
  );
}

/* ---------------------------------------------------------------- glyph level */

/**
 * Letter-spacing is a layout property, not a painting context, so the gradient survives
 * it. The glyphs themselves close up, and because the brackets are flex siblings they
 * follow the address inward without being told to.
 */
export function Tighten() {
  return (
    <Demo
      run={(b) => {
        for (const el of b.querySelectorAll('.demo-text, .demo-glow')) {
          el.animate(
            [
              { letterSpacing: '-0.02em' },
              { letterSpacing: '-0.09em', offset: 0.25 },
              { letterSpacing: '-0.02em' },
            ],
            { duration: 320, easing: 'cubic-bezier(0.3, 0, 0, 1)' },
          );
        }
      }}
    />
  );
}

/**
 * The address does not move at all. The light behind it does: a ripple runs left to
 * right through the blurred copy, which is flat colour and therefore free to be split
 * per character and animated.
 */
export function GlowWave() {
  return (
    <Demo
      run={(b) => {
        const chars = b.querySelectorAll('.glow-ch');
        chars.forEach((ch, i) => {
          ch.animate(
            [
              { transform: 'translateY(0) scale(1)', opacity: 1 },
              { transform: 'translateY(-9px) scale(1.25)', opacity: 1, offset: 0.4 },
              { transform: 'translateY(0) scale(1)', opacity: 1 },
            ],
            { duration: 420, delay: i * 22, easing: 'ease-out' },
          );
        });
      }}
      className="v-wave"
      splitGlow
    />
  );
}
