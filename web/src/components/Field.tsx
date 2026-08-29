import { Row } from "@app/components/Row";

/**
 * A row that names a setting and carries the control that changes it.
 *
 * Written down once because the alternative is what it replaced — each row laying itself
 * out, so a row whose control was a checkbox, two selects and the word "and" wrapped its
 * last select onto a line of its own while the row above it sat in a tidy justify-between.
 * Rows that lay themselves out drift apart; rows that share a component cannot.
 *
 * Anything needing the full width — a pair of selects, an explanation — goes underneath.
 */
export function Field({
  label,
  control,
  children,
}: {
  label: string;
  control?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <Row>
      <div className="flex min-h-9 items-center justify-between gap-4">
        <span className="text-sm text-fg">{label}</span>
        {control}
      </div>
      {children && <div className="space-y-2 pt-3">{children}</div>}
    </Row>
  );
}
