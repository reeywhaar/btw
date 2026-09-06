/**
 * A bin, at the size of the text beside it.
 *
 * The one mark on a reminder that ends it. It replaced a tick and a cross that did the same
 * thing as each other — *done* and *drop*, a to-do list's words, and btw is not one. The second
 * existed only so that finishing something never started did not mean claiming otherwise, and a
 * bin claims neither.
 *
 * It is also honest about where the thing goes, which the cross was not: a binned reminder is
 * kept, listed, and can be taken back out.
 */
export function BinIcon({ className = "" }: { className?: string }) {
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
      <path d="M2.6 4.2h10.8" />
      <path d="M6.2 4.2V2.6h3.6v1.6" />
      <path d="M3.9 4.2l.7 8.6a.9.9 0 0 0 .9.8h5a.9.9 0 0 0 .9-.8l.7-8.6" />
    </svg>
  );
}
