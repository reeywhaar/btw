/**
 * A cross, at the size of the text around it.
 *
 * Not a bin: dropping a reminder is deciding you do not want it, which is an answer rather
 * than a deletion — the record of it stays, and a bin would promise otherwise.
 */
export function CrossIcon({ className = "" }: { className?: string }) {
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
      <path d="M4.2 4.2 11.8 11.8" />
      <path d="M11.8 4.2 4.2 11.8" />
    </svg>
  );
}
