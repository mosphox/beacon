import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import { Bloom, Counter, Depth, Display } from './variants';
import './lab.css';

export const metadata: Metadata = {
  title: 'Beacon — hero variants',
  robots: { index: false, follow: false },
};

const VARIANTS = [
  {
    id: 'bloom',
    name: 'Bloom',
    note: 'JetBrains Mono, much larger. A blurred copy behind does the glowing; glyphs resolve out of blur on load.',
    render: <Bloom />,
  },
  {
    id: 'counter',
    name: 'Counter',
    note: 'Smaller and tighter, white rather than gradient. Digits settle like a mechanical counter; separators never move.',
    render: <Counter />,
  },
  {
    id: 'display',
    name: 'Display',
    note: 'Martian Mono — wider, more technical. Set small and tracked out, letter-spacing closing on load.',
    render: <Display />,
  },
  {
    id: 'depth',
    name: 'Depth',
    note: 'Address quiet and crisp; the field behind it carries the colour and leans toward the pointer.',
    render: <Depth />,
  },
];

export default function LabPage() {
  // Exploration surface, not part of the product. The Go backend forwards unmatched
  // browser navigations to Next, so without this it would be publicly reachable.
  if (process.env.NEXT_PUBLIC_USE_MOCK !== '1') {
    notFound();
  }

  return (
    <main className="lab">
      <header className="lab-head">
        <h1>Hero variants</h1>
        <p>
          Four treatments for the address, on the richer background. Scroll through; reload to
          replay the entrances.
        </p>
      </header>

      {VARIANTS.map((v) => (
        <section className="lab-slide" key={v.id} id={v.id}>
          <div className="lab-stage">{v.render}</div>
          <div className="lab-caption">
            <h2>{v.name}</h2>
            <p>{v.note}</p>
          </div>
        </section>
      ))}
    </main>
  );
}
