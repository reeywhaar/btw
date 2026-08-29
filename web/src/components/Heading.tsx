/** The small-caps label above a group. Used by [Section], and on its own where a heading
 *  has no rows to group — one action does not need a box drawn round it. */
export function Heading({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="px-1 pb-2 text-xs font-medium tracking-widest text-faint uppercase">
      {children}
    </h2>
  );
}
