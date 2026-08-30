import { useId } from "react";

/**
 * A labelled multi-line field.
 *
 * Separate from [TextField] rather than a variant of it: a textarea is a different element
 * with different attributes, and the branch inside one component would be longer than the
 * duplication between two.
 */
export function TextArea({
  label,
  hint,
  ...rest
}: React.ComponentProps<"textarea"> & {
  label: string;
  hint?: React.ReactNode;
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
      <textarea
        id={id}
        rows={4}
        aria-describedby={hint ? hintId : undefined}
        className="resize-y rounded-lg border border-line bg-bg px-3 py-2.5 text-fg placeholder:text-faint focus:border-accent/60 focus:outline-none"
        {...rest}
      />
    </div>
  );
}
