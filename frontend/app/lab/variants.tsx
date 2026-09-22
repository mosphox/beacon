'use client';

import { animate } from 'motion/react';
import { useEffect, useRef, useState } from 'react';

const ADDRESS = '149.3.33.84';

/**
 * useCopied returns a flag that goes true on click and clears itself.
 *
 * Nothing here writes to the clipboard except where the variant is specifically about
 * the copy itself — these are visual studies and a lab page should not quietly replace
 * what is on someone's clipboard while they browse it.
 */
function useCopied(ms = 1600) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), []);

  function fire() {
    setCopied(true);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), ms);
  }
  return [copied, fire] as const;
}

/**
 * Address is the hero treatment: a gradient clipped to the glyphs over a blurred
 * duplicate that glows, with the brackets that answer a hover.
 *
 * The address element can never be transformed, masked, or given a per-child opacity —
 * that puts it in its own painting context and the clipped gradient stops compositing.
 * Every click response below therefore works on the glow, on a sibling, or on paint
 * properties of the text.
 */
function Address({ text = ADDRESS }: { text?: string }) {
  return (
    <>
      <span className="ip-bracket left" aria-hidden="true">
        [
      </span>
      <span className="demo-stack">
        <span className="demo-glow" aria-hidden="true">
          {text}
        </span>
        <span className="demo-text" aria-hidden="true">
          {text}
        </span>
      </span>
      <span className="ip-bracket right" aria-hidden="true">
        ]
      </span>
      <span className="sr-only">{text}</span>
    </>
  );
}

/* ============================================================ click responses */

/** A ring expands out of the address and fades, the way a tap leaves a mark. */
export function Ripple() {
  const [on, fire] = useCopied(700);
  return (
    <button type="button" className={`demo-btn c-ripple ${on ? 'go' : ''}`} onClick={fire}>
      <span className="ring" aria-hidden="true" />
      <Address />
    </button>
  );
}

/** The glow flares white-hot for a moment, then settles back. */
export function Flare() {
  const ref = useRef<HTMLButtonElement>(null);

  function fire() {
    const glow = ref.current?.querySelector('.demo-glow');
    if (!glow) return;
    animate(
      glow,
      { opacity: [0.55, 1, 0.55], filter: ['blur(22px)', 'blur(10px)', 'blur(22px)'] },
      { duration: 0.7, ease: [0.2, 0, 0, 1] },
    );
  }

  return (
    <button type="button" className="demo-btn c-flare" ref={ref} onClick={fire}>
      <Address />
    </button>
  );
}

/** The brackets snap shut against the address and spring back. */
export function Snap() {
  const [on, fire] = useCopied(420);
  return (
    <button type="button" className={`demo-btn c-snap ${on ? 'go' : ''}`} onClick={fire}>
      <Address />
    </button>
  );
}

/** The address itself becomes the confirmation, then returns. */
export function InPlace() {
  const [on, fire] = useCopied(1300);
  return (
    <button type="button" className={`demo-btn c-inplace ${on ? 'go' : ''}`} onClick={fire}>
      <Address text={on ? 'copied' : ADDRESS} />
    </button>
  );
}

/** The gradient washes once through the glyphs, left to right. */
export function Wash() {
  const [on, fire] = useCopied(900);
  return (
    <button type="button" className={`demo-btn c-wash ${on ? 'go' : ''}`} onClick={fire}>
      <Address />
    </button>
  );
}

/* ============================================================== notifications */

/* Each variant owns its own state. A render prop would have to cross the server/client
   boundary from the page, and functions cannot be passed to a Client Component. */

function Trigger({ onFire }: { onFire: () => void }) {
  return (
    <button type="button" className="demo-btn" onClick={onFire}>
      <Address />
    </button>
  );
}

/** What ships today: a pill at the top of the window. */
export function TopPill() {
  const [open, fire] = useCopied(1900);
  return (
    <div className="note-stage">
      {open ? (
        <span className="note note-pill" aria-live="polite">
          Copied to clipboard
        </span>
      ) : null}
      <Trigger onFire={fire} />
    </div>
  );
}

/** A line under the address, in its own space, so nothing overlaps or shifts. */
export function UnderLine() {
  const [open, fire] = useCopied(1900);
  return (
    <div className="note-stage column">
      <Trigger onFire={fire} />
      <span className={`note note-under ${open ? 'open' : ''}`} aria-live="polite">
        Copied
      </span>
    </div>
  );
}

/** The label above stops naming the value and reports what happened. */
export function LabelSwap() {
  const [open, fire] = useCopied(1900);
  return (
    <div className="note-stage column">
      <span className="note-label" aria-live="polite">
        <span className={open ? 'off' : ''}>Your IP address</span>
        <span className={open ? 'on' : ''}>Copied to clipboard</span>
      </span>
      <Trigger onFire={fire} />
    </div>
  );
}

/** A tick slides out beside the address and holds, then leaves. */
export function Tick() {
  const [open, fire] = useCopied(1900);
  return (
    <div className="note-stage">
      <Trigger onFire={fire} />
      <span className={`note note-tick ${open ? 'open' : ''}`} aria-live="polite">
        <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
          <path
            d="M4 9.5 7.5 13 14 5.5"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        Copied
      </span>
    </div>
  );
}

/** A bar across the bottom, the way a terminal reports status. */
export function StatusBar() {
  const [open, fire] = useCopied(1900);
  return (
    <div className="note-stage">
      <Trigger onFire={fire} />
      <span className={`note note-bar ${open ? 'open' : ''}`} aria-live="polite">
        <span className="dot" aria-hidden="true" />
        {ADDRESS} copied to clipboard
      </span>
    </div>
  );
}

/** Nothing announced. The address confirms it and the moment passes. */
export function Silent() {
  const [open, fire] = useCopied(1300);
  return (
    <div className="note-stage">
      <button type="button" className={`demo-btn c-inplace ${open ? 'go' : ''}`} onClick={fire}>
        <Address text={open ? 'copied' : ADDRESS} />
      </button>
    </div>
  );
}
