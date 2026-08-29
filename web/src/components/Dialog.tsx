import {
  createContext,
  useContext,
  useEffect,
  useRef,
  type RefObject,
} from "react";

import { lockScroll } from "@app/components/scrollLock";

/**
 * The dialog a piece of the tree is inside, if it is inside one.
 *
 * Only so that a dialog opened from within another can hold that one still — see the effect
 * below. A ref rather than the element, because the provider is the dialog itself and its
 * element does not exist until after the first render; by the time a child's effect runs and
 * reads `.current`, it does.
 */
const DialogContext = createContext<RefObject<HTMLDialogElement | null> | null>(
  null,
);

/**
 * A modal, on the native `<dialog>` element.
 *
 * Native rather than a div with `role="dialog"`: `showModal()` brings focus trapping,
 * Escape, inertness of the rest of the page, and top-layer stacking that no z-index can lose
 * an argument with. Every one of those is tedious to reimplement and easy to reimplement
 * slightly wrong.
 *
 * Controlled — the caller owns `open` — because the element's own open state is DOM state,
 * and two sources of truth for one boolean is how a dialog ends up shut in React and open on
 * screen.
 */
export function Dialog({
  open,
  onClose,
  title,
  children,
  footer,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
  /**
   * What this dialog does, as buttons.
   *
   * Laid out here rather than at each call site, so that which button saves is something to
   * know rather than something to look for. The order is: whatever dismisses, then the one
   * that acts, along the right.
   */
  footer?: React.ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const parent = useContext(DialogContext);

  // Where the press started. A click on the backdrop targets the <dialog> itself — but so
  // does one that began on text inside and finished outside, which is what selecting a
  // value and dragging past the edge looks like. Requiring both ends on the backdrop is the
  // difference between closing on purpose and closing by accident.
  const startedOnBackdrop = useRef(false);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    // Guarded both ways: showModal on an open dialog throws, and close on a shut one fires
    // a second close event that would call onClose again.
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  }, [open]);

  /**
   * Nothing behind this scrolls while it is open — not the page, and not the dialog this one
   * was opened from.
   *
   * Both are needed and neither covers the other. Holding the page still leaves a parent
   * dialog free to scroll away underneath; holding the parent still leaves the page free to
   * scroll behind both.
   *
   * Separate from the effect that opens the dialog, because this one has to undo itself. An
   * unmount while open — a route change, a parent deciding to stop rendering it — would
   * otherwise leave the page locked with nothing on screen to explain why.
   */
  useEffect(() => {
    if (!open) return;

    const releasePage = lockScroll(document.body);
    const above = parent?.current;
    const releaseParent = above ? lockScroll(above) : undefined;

    return () => {
      releaseParent?.();
      releasePage();
    };
  }, [open, parent]);

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      // Escape fires `cancel` before `close`. Preventing the default and closing through the
      // same path means there is one way out, not two that can drift apart.
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onPointerDown={(e) => {
        startedOnBackdrop.current = e.target === e.currentTarget;
      }}
      onClick={(e) => {
        if (startedOnBackdrop.current && e.target === e.currentTarget)
          onClose();
        startedOnBackdrop.current = false;
      }}
      // `m-auto` is load-bearing. A modal <dialog> is centred by the user agent with
      // `inset: 0; margin: auto`, and Tailwind's preflight resets `margin: 0` on every
      // element — which leaves only the inset, and drops the dialog in the top-left corner.
      //
      // `overscroll-contain` stops a scroll that has reached the end of this dialog carrying
      // on into whatever is behind it. Holding the page still covers that for a wheel; this
      // is for touch, where `overflow: hidden` on the body is not reliably enough on its own
      // and the gesture becomes a rubber-band or a pull-to-refresh.
      className="m-auto max-h-[85dvh] w-[min(28rem,calc(100vw-2rem))] overflow-y-auto overscroll-contain rounded-xl border border-line bg-surface p-0 text-fg backdrop:bg-black/50"
    >
      {/* Unmounted while closed, so a form inside starts empty each time it is opened
          rather than holding whatever was typed and abandoned last time. */}
      {open ? (
        <DialogContext.Provider value={ref}>
          <div className="flex flex-col gap-4 px-5 pt-5 pb-4">
            <h2 className="text-lg font-semibold">{title}</h2>
            {children}
          </div>
          {footer ? (
            <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line px-5 pt-4 pb-5">
              {footer}
            </div>
          ) : null}
        </DialogContext.Provider>
      ) : null}
    </dialog>
  );
}
