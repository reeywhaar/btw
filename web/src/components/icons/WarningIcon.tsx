/**
 * A triangle with a bar in it, at the size of the text beside it.
 *
 * `1em` and `currentColor`: it sits next to a sentence, and an icon drawn at a fixed size is
 * an icon that looks wrong at every size but the one it was drawn for.
 */
export function WarningIcon({ className = "" }: { className?: string }) {
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
      // Decorative: the sentence beside it says the same thing, and a screen reader that
      // announced both would say it twice.
      aria-hidden="true"
      className={className}
    >
      <path d="M8 1.8 1.2 13.6h13.6L8 1.8Z" />
      <path d="M8 6.2v3.4" />
      <path d="M8 11.7h.01" />
    </svg>
  );
}
