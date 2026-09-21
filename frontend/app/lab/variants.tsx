'use client';

import { animate, stagger, useReducedMotion } from 'motion/react';
import { useEffect, useRef, useState } from 'react';

const ADDRESS = '149.3.33.84';
const GLYPHS = '0123456789';

/**
 * Splits an address into characters so each can be animated independently.
 * Separators are marked so they can be held back from the motion.
 */
function chars(value: string) {
  return value.split('').map((c, i) => ({ c, i, sep: c === '.' || c === ':' }));
}

/* ------------------------------------------------------------------ A: bloom */

/**
 * The address arrives as light: each glyph fades up out of a blur, and a second
 * copy sits behind it, heavily blurred, doing the glowing.
 */
export function Bloom() {
  const root = useRef<HTMLDivElement>(null);
  const reduced = useReducedMotion();

  useEffect(() => {
    if (reduced || !root.current) return;
    const glyphs = root.current.querySelectorAll('[data-glyph]');
    animate(
      glyphs,
      { opacity: [0, 1], filter: ['blur(12px)', 'blur(0px)'], y: [14, 0] },
      { duration: 0.7, delay: stagger(0.045), ease: [0.2, 0, 0, 1] },
    );
  }, [reduced]);

  return (
    <div className="v-bloom" ref={root}>
      <span className="v-bloom-glow" aria-hidden="true">
        {ADDRESS}
      </span>
      <span className="v-bloom-text">
        {chars(ADDRESS).map(({ c, i }) => (
          <span data-glyph key={i}>
            {c}
          </span>
        ))}
      </span>
    </div>
  );
}

/* ---------------------------------------------------------------- B: counter */

/**
 * Digits settle like a mechanical counter. The separators never move, so the
 * address stays readable as the numbers find their values.
 */
export function Counter() {
  const [shown, setShown] = useState(ADDRESS);
  const reduced = useReducedMotion();

  useEffect(() => {
    if (reduced) return;
    let frame = 0;
    const total = 26;
    const id = setInterval(() => {
      frame += 1;
      if (frame >= total) {
        setShown(ADDRESS);
        clearInterval(id);
        return;
      }
      // Each glyph locks in turn, left to right.
      const locked = Math.floor((frame / total) * ADDRESS.length);
      setShown(
        ADDRESS.split('')
          .map((c, i) =>
            i < locked || c === '.' ? c : GLYPHS[Math.floor(Math.random() * GLYPHS.length)],
          )
          .join(''),
      );
    }, 45);
    return () => clearInterval(id);
  }, [reduced]);

  return (
    <div className="v-counter">
      <span className="v-counter-text">{shown}</span>
      <span className="v-counter-rule" aria-hidden="true" />
    </div>
  );
}

/* ---------------------------------------------------------------- C: display */

/**
 * A wider, more editorial face, set small and tracked out. The address reads as
 * a specimen rather than a readout — quiet, but unmistakably deliberate.
 */
export function Display() {
  const root = useRef<HTMLDivElement>(null);
  const reduced = useReducedMotion();

  useEffect(() => {
    if (reduced || !root.current) return;
    const glyphs = root.current.querySelectorAll('[data-glyph]');
    animate(
      glyphs,
      { opacity: [0, 1], letterSpacing: ['0.4em', '0.08em'] },
      { duration: 0.9, delay: stagger(0.03), ease: [0.2, 0, 0, 1] },
    );
  }, [reduced]);

  return (
    <div className="v-display" ref={root}>
      <span className="v-display-text">
        {chars(ADDRESS).map(({ c, i }) => (
          <span data-glyph key={i}>
            {c}
          </span>
        ))}
      </span>
    </div>
  );
}

/* ------------------------------------------------------------------ D: depth */

/**
 * The address is quiet and crisp; the field behind it does the work, drifting
 * slowly and leaning toward the pointer.
 */
export function Depth() {
  const root = useRef<HTMLDivElement>(null);
  const reduced = useReducedMotion();

  useEffect(() => {
    const el = root.current;
    if (reduced || !el) return;

    animate(
      el.querySelectorAll('[data-glyph]'),
      { opacity: [0, 1], y: [8, 0] },
      { duration: 0.5, delay: stagger(0.03) },
    );

    function onMove(e: PointerEvent) {
      const r = el!.getBoundingClientRect();
      const x = (e.clientX - r.left) / r.width - 0.5;
      const y = (e.clientY - r.top) / r.height - 0.5;
      el!.style.setProperty('--lean-x', `${x * 16}px`);
      el!.style.setProperty('--lean-y', `${y * 16}px`);
    }
    window.addEventListener('pointermove', onMove);
    return () => window.removeEventListener('pointermove', onMove);
  }, [reduced]);

  return (
    <div className="v-depth" ref={root}>
      <span className="v-depth-field" aria-hidden="true" />
      <span className="v-depth-text">
        {chars(ADDRESS).map(({ c, i }) => (
          <span data-glyph key={i}>
            {c}
          </span>
        ))}
      </span>
    </div>
  );
}
