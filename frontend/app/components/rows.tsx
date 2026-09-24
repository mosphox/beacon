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
