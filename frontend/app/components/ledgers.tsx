import { Fragment, useId } from 'react';
import type { CSSProperties } from 'react';

export type LedgerRow = {
  key: string;
  label: string;
  /** Null is an answer: the source covers this field and has nothing for this address. */
  value: string | null;
  /** Another source that covers this field answers it differently. */
  differs: boolean;
  /** The first row of the next kind of fact: the place after the network, the registration after the place. */
  opens: boolean;
  /** An identifier or a name, which a page translator would garble: an AS, an operator, a time zone. */
  literal: boolean;
};

export type Ledger = { source: string; kind: string | null; rows: LedgerRow[] };

/**
 * A measured value. A time zone breaks after a slash, so "America/Argentina/Buenos_Aires"
 * wraps at a slash rather than mid-word; everything else relies on `overflow-wrap:
 * anywhere`, which is what keeps a 58-letter Welsh place name inside its ledger.
 */
function Value({ value }: { value: string }) {
  if (!value.includes('/')) return <>{value}</>;
  const parts = value.split('/');
  return (
    <>
      {parts.map((p, i) => (
        <Fragment key={i}>
          {p}
          {i < parts.length - 1 ? (
            <>
              /<wbr />
            </>
          ) : null}
        </Fragment>
      ))}
    </>
  );
}

/**
 * Ledgers set each source's whole answer down in one list, and the lists side by side.
 *
 * A ledger holds only the fields its source covers, so nothing in one is a placeholder.
 * The rows still line up across ledgers, because the fields come in one order everywhere
 * and that order is chosen so each database's fields run unbroken from the top: see
 * LEDGER_FIELDS. The ledgers in a row of the grid also share their row lines (subgrid), so
 * a value that wraps in one ledger moves the rules under it in all of them, and the
 * alignment survives a long name.
 *
 * How many sit in a row is the CSS's decision: as many as fit, then evened out, so six
 * where four would fit go three and three, never four and two.
 */
export function Ledgers({ ledgers }: { ledgers: Ledger[] }) {
  const id = useId();
  const longest = Math.max(0, ...ledgers.map((l) => l.rows.length));

  return (
    <div className="ledgers">
      <div
        className="ledger-grid"
        /* Genuinely runtime-computed: the number of ledgers decides how many share a row,
           and the longest decides how many row lines each one spans. */
        style={{ '--n': ledgers.length, '--span': longest + 1 } as CSSProperties}
      >
        {ledgers.map((l, i) => (
          <div className="ledger" key={l.source} role="group" aria-labelledby={`${id}-${i}`}>
            <div className="ledger-head">
              <h3 className="ledger-name" id={`${id}-${i}`} translate="no">
                {l.source}
              </h3>
              {l.kind ? <p className="ledger-kind">{l.kind}</p> : null}
            </div>
            <dl className="ledger-rows">
              {l.rows.map((r) => (
                <div
                  key={r.key}
                  className={['ledger-row', r.differs ? 'differs' : '', r.opens ? 'opens' : '']
                    .filter(Boolean)
                    .join(' ')}
                >
                  <dt>
                    {r.label}
                    {r.differs ? <span className="sr-only"> (the sources differ)</span> : null}
                  </dt>
                  {r.value === null ? (
                    <dd className="absent">not available</dd>
                  ) : (
                    <dd translate={r.literal ? 'no' : undefined}>
                      <Value value={r.value} />
                    </dd>
                  )}
                </div>
              ))}
            </dl>
          </div>
        ))}
      </div>
    </div>
  );
}
