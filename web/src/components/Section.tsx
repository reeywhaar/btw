import { Heading } from "@app/components/Heading";

/**
 * A titled group of rows.
 *
 * For rows. A section whose only content is one button should be a [Heading] and that
 * button — a bordered card drawn around a single control is a box inside a box, and says
 * nothing except that somebody had a container to hand.
 *
 * Bordered and divided, because a settings screen without visible grouping is a flat stream
 * of labels where nothing says which header a row belongs to. The hairlines between rows do
 * the other half of the job — they carry the eye from a name on the left to its control on
 * the right, which at this column width nothing else does.
 *
 * Everything inside one should be a Row, or something built out of Rows.
 */
export function Section({
  title,
  children,
  footer,
}: {
  title: string;
  children: React.ReactNode;
  /** Commentary that belongs to the section but is not a setting, shown under the group. */
  footer?: React.ReactNode;
}) {
  return (
    <section>
      <Heading>{title}</Heading>
      <div className="divide-y divide-line overflow-hidden rounded-xl border border-line bg-surface">
        {children}
      </div>
      {footer && <div className="px-1 pt-2">{footer}</div>}
    </section>
  );
}
