/**
 * A cross inside a circle, at the size of the text beside it.
 *
 * Distinct from [CrossIcon], which is the bare cross a person presses to drop a reminder.
 * That one is an action somebody takes; this one is a state something is in, and drawing them
 * the same shape would make a report look like a button.
 */
export function CrossCircleIcon({ className = "" }: { className?: string }) {
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
      // Decorative: the sentence beside it says the same thing, and a screen reader that
      // announced both would say it twice.
      aria-hidden="true"
      className={className}
    >
      <circle cx="8" cy="8" r="6.2" />
      <path d="M5.9 5.9 10.1 10.1" />
      <path d="M10.1 5.9 5.9 10.1" />
    </svg>
  );
}
