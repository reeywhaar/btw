/**
 * A person, drawn as their own initial rather than as a pictogram of a person.
 *
 * The generic head-and-shoulders glyph is the obvious thing here and it was the wrong one:
 * at nav size a stroked figure is a few spindly curves that read as clip art, and it says
 * "a person" next to text already naming which one.
 *
 * SVG rather than a letter centred in a rounded `<span>`, which is the obvious way and is
 * off by about a third of a pixel. A line box carries room for descenders, and a capital
 * has none — so centring the *box* leaves the ink riding high, by an amount that looks like
 * nothing in the markup and like a mistake on the screen. Measured: 0.38px up at 20px.
 *
 * `dy=".35em"` from the geometric centre is the long-standing way to centre a capital on
 * its own cap height, and it does not depend on the element's line box at all.
 */
export function Avatar({ name }: { name: string }) {
  // Array.from rather than slice, so a first character outside the basic plane is not cut
  // in half. Usernames here are letters, digits and - _ . — but a component should not
  // depend on its caller's validation.
  const initial = (Array.from(name)[0] ?? "?").toUpperCase();

  return (
    <svg
      viewBox="0 0 20 20"
      width="1.25em"
      height="1.25em"
      // Decorative: the name is right beside it, and a screen reader announcing both would
      // read the first letter and then the whole word.
      aria-hidden="true"
      className="shrink-0"
    >
      <circle cx="10" cy="10" r="10" className="fill-muted" />
      <text
        x="10"
        y="10"
        dy=".35em"
        textAnchor="middle"
        className="fill-bg text-[9px] font-semibold"
      >
        {initial}
      </text>
    </svg>
  );
}
