import { useId } from "react";

/**
 * A labelled input.
 *
 * The label is bound by a generated id rather than by wrapping, so a hint can sit between
 * the two without ending up inside the label and being read out as part of it.
 */
export function TextField({
  label,
  hint,
  error,
  ...rest
}: React.ComponentProps<"input"> & {
  label: string;
  hint?: React.ReactNode;
  error?: string;
}) {
  const id = useId();
  const hintId = `${id}-hint`;

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-fg">
        {label}
      </label>
      {hint ? (
        <p id={hintId} className="text-xs text-faint">
          {hint}
        </p>
      ) : null}
      <input
        id={id}
        aria-describedby={hint ? hintId : undefined}
        aria-invalid={error ? true : undefined}
        // No text size here: main.css holds every input at 16px so iOS does not zoom the
        // page on focus, and a text-sm would read like it applied when it does not.
        className="rounded-lg border border-line bg-bg px-3 py-2.5 text-fg placeholder:text-faint focus:border-accent/60 focus:outline-none"
        {...rest}
      />
      {error ? <p className="text-xs text-accent">{error}</p> : null}
    </div>
  );
}
