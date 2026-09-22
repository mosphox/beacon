import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import {
  Flare,
  InPlace,
  LabelSwap,
  Ripple,
  Silent,
  Snap,
  StatusBar,
  Tick,
  TopPill,
  UnderLine,
  Wash,
} from './variants';
import './lab.css';

export const metadata: Metadata = {
  title: 'Beacon — click and notification variants',
  robots: { index: false, follow: false },
};

const CLICKS = [
  {
    id: 'ripple',
    name: 'Ripple',
    note: 'A ring expands out of the address and fades, the way a tap leaves a mark. Reads as "received" without saying anything.',
    render: <Ripple />,
  },
  {
    id: 'flare',
    name: 'Flare',
    note: 'The glow behind the address flares and settles. The address never moves; only the light around it reacts.',
    render: <Flare />,
  },
  {
    id: 'snap',
    name: 'Brackets snap shut',
    note: 'The brackets already there for hover close hard against the address and spring back — the same object doing two jobs.',
    render: <Snap />,
  },
  {
    id: 'inplace',
    name: 'Becomes the confirmation',
    note: 'The address turns green and reads "copied", then returns. Strongest signal of the set, at the cost of hiding the value for a moment.',
    render: <InPlace />,
  },
  {
    id: 'wash',
    name: 'Colour washes through',
    note: 'One pass of the gradient across the glyphs, left to right. Quietest of the five.',
    render: <Wash />,
  },
];

const NOTES = [
  {
    id: 'pill',
    name: 'Pill at the top',
    note: 'What ships today. Clear, but it appears a long way from where the click happened.',
    render: <TopPill />,
  },
  {
    id: 'under',
    name: 'A line underneath',
    note: 'Directly below the address, holding its own space so nothing shifts when it appears.',
    render: <UnderLine />,
  },
  {
    id: 'label',
    name: 'The label reports it',
    note: 'The label above stops naming the value and says what happened, then changes back. No new element enters the page.',
    render: <LabelSwap />,
  },
  {
    id: 'tick',
    name: 'A tick alongside',
    note: 'A check mark slides out beside the address and holds. Reads instantly, says nothing to read.',
    render: <Tick />,
  },
  {
    id: 'bar',
    name: 'Status bar',
    note: 'A bar across the bottom of the panel, the way a terminal reports. Fits the instrument voice and names the value it copied.',
    render: <StatusBar />,
  },
  {
    id: 'silent',
    name: 'No notification',
    note: 'The address confirms it and the moment passes. Nothing to dismiss, nothing to time out.',
    render: <Silent />,
  },
];

export default function LabPage() {
  if (process.env.NEXT_PUBLIC_USE_MOCK !== '1') {
    notFound();
  }

  return (
    <main className="lab">
      <header className="lab-head">
        <h1>Click and notification variants</h1>
        <p>
          Click each address. The first group is what the address itself does; the second is
          how the copy gets announced. They combine — pick one from each.
        </p>
      </header>

      <div className="lab-group">
        <h2>What the address does</h2>
        <p>
          Every one of these works on the glow, on a sibling, or on paint properties of the
          text. The address cannot be transformed or masked without its clipped gradient
          falling apart, so none of them move it.
        </p>
      </div>
      {CLICKS.map((v) => (
        <section className="lab-slide" key={v.id} id={v.id}>
          <div className="lab-stage">{v.render}</div>
          <div className="lab-caption">
            <h3>{v.name}</h3>
            <p>{v.note}</p>
          </div>
        </section>
      ))}

      <div className="lab-group">
        <h2>How it gets announced</h2>
        <p>
          All of these use the bracket hover and no click animation, so you are judging the
          notification alone.
        </p>
      </div>
      {NOTES.map((v) => (
        <section className="lab-slide" key={v.id} id={v.id}>
          <div className="lab-stage">{v.render}</div>
          <div className="lab-caption">
            <h3>{v.name}</h3>
            <p>{v.note}</p>
          </div>
        </section>
      ))}
    </main>
  );
}
