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
  quiet:
    "rounded-lg border border-line px-4 py-2.5 text-sm text-muted hover:border-line-strong hover:text-fg disabled:opacity-50",
  link: "text-left text-sm text-muted underline-offset-4 hover:text-fg hover:underline",
} as const;

export function Button({
  variant = "solid",
  ...props
}: { variant?: keyof typeof variants } & React.ComponentProps<"button">) {
  return <button {...props} className={variants[variant]} />;
}
