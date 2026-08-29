import { WarningIcon } from "@app/components/icons/WarningIcon";

/**
 * A caution: something is not working, and it is not an error anybody made.
 *
 * The mark is coloured and the sentence is not. A whole paragraph set in warning colour
 * shouts, and the thing being said here is usually mild — this browser cannot, that
 * permission is off. The icon is enough to catch an eye scanning the page.
 */
export function Warning({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex gap-2.5">
      <WarningIcon className="mt-px shrink-0 text-base text-warn" />
      <p className="text-sm text-muted">{children}</p>
    </div>
  );
}
