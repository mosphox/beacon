'use client';

import { useRef, useState } from 'react';

const ADDRESS = '149.3.33.84';

/**
 * Address renders the chosen hero treatment: a gradient clipped to the glyphs over a
 * blurred duplicate that does the glowing.
 *
 * The address element itself must never be transformed, masked, or given a per-child
 * opacity — that puts it in its own painting context and the clipped gradient stops
 * compositing. Every hover below therefore works on the glow, on a sibling, or on paint
 * properties of the text (colour, background-position), never on its geometry.
 */
function Address({ className = '' }: { className?: string }) {
  return (
    <span className={`demo-ip ${className}`}>
      <span className="demo-ip-glow" aria-hidden="true">
        {ADDRESS}
      </span>
      <span className="demo-ip-text" aria-hidden="true">
        {ADDRESS}
      </span>
      <span className="sr-only">{ADDRESS}</span>
    </span>
  );
}

/* --------------------------------------------------- A: the gradient travels */

/**
 * The address holds still; its colour moves through it. The gradient repeats its
 * sequence so any window onto it reads the same, which means it can slide without the
 * resting state looking different from the chosen hero.
 */
export function Travel() {
  return (
    <button type="button" className="demo-btn h-travel">
      <Address />
    </button>
  );
}

/* ------------------------------------------------------- B: selection block */

/** A terminal selection behind the glyphs — the shape of the thing you are about to copy. */
export function Selection() {
  return (
    <button type="button" className="demo-btn h-select">
      <Address />
    </button>
  );
}

/* ------------------------------------------------------------- C: brackets */

/** Two brackets close in around the address, the way a prompt marks a value. */
export function Brackets() {
  return (
    <button type="button" className="demo-btn h-brackets">
      <span className="bracket left" aria-hidden="true">
        [
      </span>
      <Address />
      <span className="bracket right" aria-hidden="true">
        ]
      </span>
    </button>
  );
}

/* ----------------------------------------------------- D: chromatic split */

/**
 * Two more glow copies, cyan and pink, drift apart under the address on hover — the
 * separation a CRT gives coloured light. The address stays perfectly still and sharp.
 */
export function Chromatic() {
  return (
    <button type="button" className="demo-btn h-chroma">
      <span className="demo-ip h-chroma-stack">
        <span className="chroma cy" aria-hidden="true">
          {ADDRESS}
        </span>
        <span className="chroma pk" aria-hidden="true">
          {ADDRESS}
        </span>
        <span className="demo-ip-text" aria-hidden="true">
          {ADDRESS}
        </span>
        <span className="sr-only">{ADDRESS}</span>
      </span>
    </button>
  );
}

/* ----------------------------------------------------------- E: label says */

/**
 * Nothing on the address at all. The label above it changes from naming the value to
 * naming the action, which is the only new information a hover actually carries.
 */
export function LabelSays() {
  return (
    <button type="button" className="demo-btn h-label">
      <span className="demo-label">
        <span className="rest">Your IP address</span>
        <span className="hover">Click to copy</span>
      </span>
      <Address />
    </button>
  );
}

/* ------------------------------------------------------------ F: real copy */

/**
 * The interaction itself as the feedback: no hover state, but clicking swaps the address
 * for a confirmation in place, then swaps back.
 */
export function InPlace() {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function onClick() {
    navigator.clipboard?.writeText(ADDRESS).catch(() => {});
    setCopied(true);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1400);
  }

  return (
    <button type="button" className="demo-btn h-inplace" onClick={onClick}>
      <span className={`demo-ip ${copied ? 'is-copied' : ''}`}>
        <span className="demo-ip-glow" aria-hidden="true">
          {ADDRESS}
        </span>
        <span className="demo-ip-text" aria-hidden="true">
          {copied ? 'copied' : ADDRESS}
        </span>
        <span className="sr-only">{ADDRESS}</span>
      </span>
    </button>
  );
}
