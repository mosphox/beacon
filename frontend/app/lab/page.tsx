import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import { Depth, GlowWave, Keypress, Pop, Punch, Settle, Squash, Tighten, Tilt } from './variants';
import './lab.css';

export const metadata: Metadata = {
  title: 'Beacon — address motion variants',
  robots: { index: false, follow: false },
};

const SCALING = [
  {
    id: 'punch',
    name: 'Punch',
    note: 'Down hard to 0.93, back past the resting size to 1.025, then settles. The overshoot is what makes it read as a physical object rather than a number changing size.',
    render: <Punch />,
  },
  {
    id: 'pop',
    name: 'Pop',
    note: 'The other direction — 1.05 and back. The value jumps toward you instead of away, which reads as the page answering rather than as you pressing.',
    render: <Pop />,
  },
  {
    id: 'squash',
    name: 'Squash and stretch',
    note: 'Wider and flatter, then narrower and taller, then rest. The volume looks preserved, so the address behaves like something with mass.',
    render: <Squash />,
  },
  {
    id: 'settle',
    name: 'Settle',
    note: 'No travel inward at all: it is already at 0.95 by the time you see it and eases back over 420ms. Asymmetric, so it feels like release rather than a round trip.',
    render: <Settle />,
  },
];

const MOVEMENT = [
  {
    id: 'keypress',
    name: 'Keypress',
    note: 'Six pixels straight down and back, the way a key travels under a finger. The only variant where the address changes position rather than size.',
    render: <Keypress />,
  },
  {
    id: 'depth',
    name: 'Depth',
    note: 'Pushed 90px into the screen under a 900px perspective. It foreshortens rather than simply shrinking, so it recedes instead of getting smaller.',
    render: <Depth />,
  },
  {
    id: 'tilt',
    name: 'Tilt',
    note: 'The top edge rotates 14° away from you, hinged at the baseline. The most physical of the set and the most obviously an effect.',
    render: <Tilt />,
  },
];

const GLYPH = [
  {
    id: 'tighten',
    name: 'Tighten',
    note: 'Letter-spacing contracts from −0.02em to −0.09em and releases. The glyphs themselves close up, and because the brackets are flex siblings they follow the address inward without being told to — the snap and the squeeze become one movement.',
    render: <Tighten />,
  },
  {
    id: 'wave',
    name: 'Glow wave',
    note: 'The address does not move at all. A ripple runs left to right through the blurred copy behind it, 22ms apart per character, so the light reacts and the value stays put.',
    render: <GlowWave />,
  },
];

const GROUPS = [
  {
    id: 'scaling',
    title: 'Scaling',
    lede: 'All four scale the button, never the address element itself. What differs is the direction, the curve and whether the movement is symmetric.',
    items: SCALING,
  },
  {
    id: 'movement',
    title: 'Movement',
    lede: 'Position and rotation rather than size. Depth and tilt both need a perspective, which is what separates them from a plain scale.',
    items: MOVEMENT,
  },
  {
    id: 'glyph',
    title: 'The glyphs themselves',
    lede: 'Two ways around the rule that the address cannot be transformed: change a property that is not a transform, or move the glow instead of the text.',
    items: GLYPH,
  },
];

export default function LabPage() {
  if (process.env.NEXT_PUBLIC_USE_MOCK !== '1') {
    notFound();
  }

  return (
    <main className="lab">
      <header className="lab-head">
        <h1>Address motion variants</h1>
        <p>
          Click each address. Every one of them also fires the bracket snap and the copy
          toast exactly as they ship, so you are judging each movement in the company it
          would actually keep — and the toast against the real background, since it is
          glass and has nothing of its own to look at.
        </p>
      </header>

      {GROUPS.map((group) => (
        <div key={group.id}>
          <div className="lab-group">
            <h2>{group.title}</h2>
            <p>{group.lede}</p>
          </div>
          {group.items.map((v) => (
            <section className="lab-slide" key={v.id} id={v.id}>
              <div className="lab-stage">{v.render}</div>
              <div className="lab-caption">
                <h3>{v.name}</h3>
                <p>{v.note}</p>
              </div>
            </section>
          ))}
        </div>
      ))}
    </main>
  );
}
