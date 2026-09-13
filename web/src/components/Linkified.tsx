/**
 * Text with whatever links are in it made clickable.
 *
 * Split into nodes rather than set as HTML. This is somebody's own writing going back onto
 * their own screen, and there is no version of `dangerouslySetInnerHTML` here that is worth
 * the one day it is not.
 *
 * Only `http` and `https`, which is what keeps `javascript:` from ever becoming an anchor.
 */
export function Linkified({ text }: { text: string }) {
  return (
    <>
      {text.split(pattern).map((part, i) => {
        // split with a capturing group alternates: even is prose, odd is a match.
        if (i % 2 === 0) return part;
        // A full stop or a bracket after a URL is punctuation somebody wrote, not part of the
        // address — and following it lands on a 404.
        const url = part.replace(/[.,;:!?)\]]+$/, "");
        return (
          <span key={i}>
            <a
              href={url}
              target="_blank"
              rel="noreferrer noopener"
              // Positioned so it sits above whatever stretched over the row to catch a press.
              // Without it the row's own button is in front and the link cannot be followed.
              className="relative underline underline-offset-2 hover:text-fg"
            >
              {url}
            </a>
            {part.slice(url.length)}
          </span>
        );
      })}
    </>
  );
}

const pattern = /(https?:\/\/[^\s<>"']+)/g;
