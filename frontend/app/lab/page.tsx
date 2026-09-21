import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import { Brackets, Chromatic, InPlace, LabelSays, Selection, Travel } from './variants';
import './lab.css';

export const metadata: Metadata = {
  title: 'Beacon — hover variants',
  robots: { index: false, follow: false },
};

const VARIANTS = [
  {
    id: 'travel',
    name: 'The gradient travels',
    note: 'The address holds still; its colour moves through it over 700ms. The gradient repeats its sequence, so the resting state is identical to the hero as it ships.',
    render: <Travel />,
  },
  {
    id: 'selection',
    name: 'Selection block',
    note: 'A terminal text-selection appears behind the glyphs — the shape of the thing you are about to copy, in the vocabulary the rest of the page already uses.',
    render: <Selection />,
  },
  {
    id: 'brackets',
    name: 'Brackets close in',
    note: 'Two brackets slide inward and fade up, the way a shell prompt marks a value. Nothing about the address itself changes.',
    render: <Brackets />,
  },
  {
    id: 'chromatic',
    name: 'Chromatic split',
    note: 'Cyan and pink copies drift apart underneath, the separation a CRT gives coloured light. The address stays sharp and still on top.',
    render: <Chromatic />,
  },
  {
    id: 'label',
    name: 'The label says it',
    note: 'No motion on the address at all. The label above stops naming the value and names the action instead — the only new information a hover actually carries.',
    render: <LabelSays />,
  },
  {
    id: 'inplace',
    name: 'No hover, answer the click',
    note: 'Nothing on hover. Clicking swaps the address for a confirmation in place and back again — feedback where the action happened, instead of a toast in the corner. Click this one.',
    render: <InPlace />,
  },
];

export default function LabPage() {
  if (process.env.NEXT_PUBLIC_USE_MOCK !== '1') {
    notFound();
  }

  return (
    <main className="lab">
      <header className="lab-head">
        <h1>Hover variants</h1>
        <p>
          Six ways the address can answer a pointer, on the hero as it ships. Hover each one;
          the last one wants a click.
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
