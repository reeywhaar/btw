/**
 * `bg-bg` rather than `bg-surface`: a select sits inside a Section, which is already the
 * raised surface, so matching it would leave the control invisible against its own row.
 */
export function Select(props: React.ComponentProps<"select">) {
  return (
    <select
      {...props}
      className="rounded-md border border-line bg-bg px-3 py-2 text-fg disabled:text-faint disabled:opacity-50"
    />
  );
}
