/** One row inside a [Section]. Owns the padding, so nothing inside has to. */
export function Row({ children }: { children: React.ReactNode }) {
  return <div className="px-4 py-3">{children}</div>;
}
