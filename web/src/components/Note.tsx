/** Explanatory text. Never a label, never an action. */
export function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-faint">{children}</p>;
}
