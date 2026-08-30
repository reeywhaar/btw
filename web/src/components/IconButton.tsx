/**
 * An action drawn as a mark rather than a word.
 *
 * No border. A bordered control beside a sentence competes with it — the row already has a
 * hairline under it and a card around it, and a third rectangle for a tick is one shape too
 * many. The target is made by padding instead, which keeps it thumb-sized without drawing
 * anything.
 *
 * `label` is required and is not decoration: the icon carries no text, so this is the only
 * thing a screen reader or a tooltip has to go on.
 */
export function IconButton({
  label,
  children,
  ...rest
}: React.ComponentProps<"button"> & { label: string }) {
  return (
    <button
      {...rest}
      aria-label={label}
      title={label}
      className="shrink-0 rounded-md p-2 text-[1.15rem] text-faint transition-colors hover:bg-line/60 hover:text-fg"
    >
      {children}
    </button>
  );
}
