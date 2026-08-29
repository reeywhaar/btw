/**
 * Three weights, and which one to reach for is the whole of the decision.
 *
 * `solid` is the one thing a screen wants you to do. `quiet` is available but not urged.
 * `link` is an action that reads as a sentence rather than a target — signing out, or
 * accepting an offer made in the label beside it.
 *
 * Inverted rather than coloured: `bg-fg text-bg` is dark-on-light in light mode and
 * light-on-dark in dark mode, from one pair of classes.
 */
const variants = {
  solid:
    "rounded-lg bg-fg px-4 py-2.5 text-sm font-medium text-bg disabled:opacity-50",
  // Filled rather than outlined. Bordered, it drew a second rounded rectangle inside the
  // one a Section already draws, and two nested borders around one control read as a
  // mistake. A fill separates it from its background without competing with it.
  quiet:
    "rounded-lg bg-line/60 px-4 py-2.5 text-sm text-fg hover:bg-line disabled:opacity-50",
  link: "text-left text-sm text-muted underline-offset-4 hover:text-fg hover:underline",
} as const;

export function Button({
  variant = "solid",
  ...props
}: { variant?: keyof typeof variants } & React.ComponentProps<"button">) {
  return <button {...props} className={variants[variant]} />;
}
