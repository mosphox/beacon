import type { ReactNode } from 'react';

export function Section({
  title,
  note,
  noteTone,
  intro,
  children,
}: {
  title: string;
  note?: string;
  noteTone?: 'agree' | 'differ';
  intro?: string;
  children: ReactNode;
}) {
  return (
    <section className="section">
      <div className="section-head">
        <h2>{title}</h2>
        {note ? <span className={`section-note ${noteTone ?? ''}`}>{note}</span> : null}
      </div>
      {intro ? <p className="section-intro">{intro}</p> : null}
      {children}
    </section>
  );
}

/**
 * Group is one table inside a section, with a caption saying what the table is about.
 *
 * Sections used to be a single undifferentiated run of rows — TLS ran to twenty in a
 * row, and nothing said where "what the client offered" ended and "what the two sides
 * agreed on" began. The caption is sentence case and quiet on purpose: it is a table
 * caption, not an eyebrow label, and the section heading above it is the loud one.
 */
export function Group({
  caption,
  note,
  children,
}: {
  caption?: string;
  note?: string;
  children: ReactNode;
}) {
  return (
    <div className="group">
      {caption ? <h3 className="group-caption">{caption}</h3> : null}
      {children}
      {note ? <p className="group-note">{note}</p> : null}
    </div>
  );
}

/**
 * Row renders one label/value pair. A null value is rendered as a stated absence rather
 * than hidden: "no source had this" is an answer, and dropping the row would make the
 * page silently shorter instead of informative.
 */
export function Row({
  label,
  value,
  absent = 'not available',
  labelMono = false,
}: {
  label: string;
  value: ReactNode;
  absent?: string;
  /** For rows whose label is itself a protocol identifier, such as a SETTINGS name. */
  labelMono?: boolean;
}) {
  const empty = value === null || value === undefined || value === '';
  return (
    <div className="row">
      <dt className={labelMono ? 'mono' : undefined}>{label}</dt>
      {empty ? <dd className="absent">{absent}</dd> : <dd>{value}</dd>}
    </div>
  );
}

export function Rows({ children }: { children: ReactNode }) {
  return <dl className="rows">{children}</dl>;
}

export type CompareRow = {
  label: string;
  /** One entry per column, in the same order as `columns`. */
  values: (string | null)[];
  /**
   * Whether each column's source provides this field at all. A column that does not is
   * outside the comparison for this row: it shows a dash and cannot mark the row as a
   * disagreement. Omitted means every column provides it.
   */
  covered?: boolean[];
};

/**
 * Compare puts the sources side by side, one column each, and marks the fields where
 * they disagree.
 *
 * This is the page's reason to exist, so it gets a real table rather than a stack of
 * attributed claims: the comparison is two-dimensional and a table is what makes
 * "MaxMind says ±1000 km, DB-IP says ±50 km" legible at a glance. It also removes an
 * older ambiguity — the merged values used to be listed unattributed underneath a
 * heading that said the sources disagreed, which left the reader no way to tell whose
 * numbers those were.
 *
 * Below 640px the same markup restyles into stacked blocks, each value prefixed with its
 * source, because three columns of place names do not fit a phone.
 *
 * Only sources that provide a field are compared on it. A source that never carries a
 * field — DB-IP Lite has no postal codes, a country-only database no coordinates — has
 * not disagreed by having nothing there, and letting its empty cell mark the row would
 * put a disagreement on nearly every row once sources of different precision sit side
 * by side.
 */
/** Letters that carry their accent in their shape, which NFD does not take apart. */
const PLAIN: Record<string, string> = {
  ø: 'o',
  ł: 'l',
  đ: 'd',
  ß: 'ss',
  æ: 'ae',
  œ: 'oe',
  ı: 'i',
  þ: 'th',
};

/**
 * A value as the comparison sees it: case and accents aside, so "Malmö" is "Malmo". A
 * geofeed is often written in plain ASCII, and a spelling is not a disagreement.
 */
function fold(v: string | null): string | null {
  if (v === null) return null;
  return v
    .normalize('NFD')
    .replace(/\p{Mn}/gu, '')
    .toLowerCase()
    .replace(/[øłđßæœıþ]/g, (c) => PLAIN[c]);
}

export function Compare({
  columns,
  rows,
  absent = 'not available',
}: {
  columns: string[];
  rows: CompareRow[];
  absent?: string;
}) {
  return (
    /* The roles are redundant on a table that renders as a table — and load-bearing on
       one that does not. The narrow variant sets `display: block` on these elements,
       which drops their implicit table roles in Chrome and Safari, so the columns stop
       being columns to a screen reader exactly where the visual columns are gone too. */
    <table className="compare" role="table">
      <thead>
        <tr role="row">
          <th scope="col" role="columnheader">
            <span className="sr-only">Field</span>
          </th>
          {columns.map((c) => (
            <th scope="col" role="columnheader" key={c} translate="no">
              {c}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const isCovered = (i: number) => r.covered?.[i] ?? true;
          const compared = r.values.filter((_, i) => isCovered(i)).map(fold);
          const differs = compared.some((v) => v !== compared[0]);
          return (
            <tr role="row" key={r.label} className={differs ? 'differs' : undefined}>
              <th scope="row" role="rowheader">
                {r.label}
              </th>
              {r.values.map((v, i) =>
                isCovered(i) ? (
                  <td
                    role="cell"
                    key={columns[i]}
                    data-source={columns[i]}
                    className={v === null ? 'absent' : undefined}
                  >
                    {v ?? absent}
                  </td>
                ) : (
                  <td role="cell" key={columns[i]} data-source={columns[i]} className="uncovered">
                    <span aria-hidden="true">—</span>
                    <span className="sr-only">not in this source’s data</span>
                  </td>
                ),
              )}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

export function Flag({ on, children }: { on: boolean; children: ReactNode }) {
  return <span className={on ? 'badge on' : 'badge'}>{children}</span>;
}

/**
 * Chips renders a long list of protocol values, collapsed by default. The full cipher
 * list runs to dozens of entries and would bury everything under it.
 */
export function Chips({
  items,
  limit = 12,
  expanded,
  onExpand,
  label,
}: {
  items: string[];
  limit?: number;
  expanded: boolean;
  onExpand: () => void;
  label: string;
}) {
  if (items.length === 0) return <span className="absent">none offered</span>;
  const shown = expanded ? items : items.slice(0, limit);
  const hidden = items.length - shown.length;

  return (
    <div className="chips">
      {shown.map((v, i) => (
        <span className="chip" key={`${v}-${i}`}>
          {v}
        </span>
      ))}
      {hidden > 0 ? (
        <button type="button" className="more" onClick={onExpand}>
          {`show ${hidden} more ${label}`}
        </button>
      ) : null}
    </div>
  );
}
