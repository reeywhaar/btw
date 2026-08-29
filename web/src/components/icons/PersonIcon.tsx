/**
 * A head and shoulders, at the size of the text beside it.
 *
 * `1em` and `currentColor`, like the rest: it sits next to a username, and an icon drawn at
 * a fixed size is an icon that looks wrong at every size but the one it was drawn for.
 */
export function PersonIcon({ className = "" }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      width="1em"
      height="1em"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
      strokeLinejoin="round"
      // Decorative: the username is right beside it, and a screen reader announcing both
      // would say the same thing twice.
      aria-hidden="true"
      className={className}
    >
      <circle cx="8" cy="5.4" r="2.8" />
      <path d="M2.6 14c.7-2.8 2.8-4.2 5.4-4.2s4.7 1.4 5.4 4.2" />
    </svg>
  );
}
