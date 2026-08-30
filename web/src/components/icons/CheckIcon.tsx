/**
 * A tick, at the size of the text around it.
 *
 * 2px stroke on a 16 viewBox, like every icon here: at the sizes these are drawn a thinner
 * line reads as grey rather than as a shape, and goes soft on a non-retina screen.
 */
export function CheckIcon({ className = "" }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      width="1em"
      height="1em"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
    >
      <path d="M3 8.5 6.4 12 13 4.5" />
    </svg>
  );
}
