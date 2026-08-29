/**
 * A row that is itself the button.
 *
 * The alternative — a link-styled button sitting inside a [Row] — puts a small piece of text
 * in the corner of a large bordered card, so the card reads as the affordance and the text
 * does not. Nothing says where to press.
 *
 * So the press target is the row: full width, its own padding, and a hover that fills it.
 *
 * `danger` is for the ones worth a second of hesitation. Signing out is not one of them —
 * you sign back in — so it stays the ordinary colour and the accent keeps meaning something.
 */
export function RowButton({
  danger,
  ...props
}: { danger?: boolean } & React.ComponentProps<"button">) {
  return (
    <button
      {...props}
      className={
        "block w-full px-4 py-3 text-left text-sm transition-colors hover:bg-line/50 " +
        (danger ? "text-accent" : "text-fg")
      }
    />
  );
}
