/**
 * A person, drawn as their own initial rather than as a pictogram of a person.
 *
 * The generic head-and-shoulders glyph is the obvious thing here and it was the wrong one:
 * at nav size a stroked figure is a few spindly curves that read as clip art, and it says
 * "a person" next to text already naming which one.
 *
 * A filled disc with a letter in it survives being small — solid shape, one glyph — and
 * belongs to the account it sits beside.
 */
export function Avatar({ name }: { name: string }) {
  // Array.from rather than slice, so a first character outside the basic plane is not cut
  // in half. Usernames here are letters, digits and - _ . — but a component should not
  // depend on its caller's validation.
  const initial = Array.from(name)[0] ?? "?";

  return (
    <span
      // Decorative: the name is right beside it, and a screen reader announcing both would
      // read the first letter and then the whole word.
      aria-hidden="true"
      className="grid size-5 shrink-0 translate-y-0.5 place-items-center rounded-full bg-muted text-[0.65rem] font-semibold text-bg uppercase"
    >
      {initial}
    </span>
  );
}
