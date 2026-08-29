import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";

import { getAuthMe } from "@app/api/actions/auth";
import { getDevices } from "@app/api/actions/devices";
import { qk } from "@app/api/keys";
import { ApiError } from "@app/api/transport";
import { Reminders } from "@app/islands/app/Reminders";
import { useRoute } from "@app/islands/app/route";
import { Settings } from "@app/islands/app/Settings";
import { pushState } from "@app/push";

/**
 * Why nothing will arrive, and whether the person can do anything about it.
 *
 * The bar used to say "Turn them on →" for every silent state, including the ones where
 * there is nothing to turn on — so somebody on a browser without push was invited to tap
 * through to a screen whose answer was "this browser cannot". A call to action that leads
 * to a dead end is worse than a plain statement, because it spends somebody's attention
 * before telling them the thing they needed to know.
 *
 * So the bar names the actual reason, and only offers the tap when there is something on
 * the other end of it.
 */
type Silence = { text: string; actionable: boolean } | null;

function silence(registered: boolean): Silence {
  // Any device at all means nudges are arriving somewhere, so there is nothing to warn
  // about. The bar means "nothing will arrive anywhere" — not "not on this screen", which
  // is an ordinary thing for a laptop to be and not worth a standing, undismissible notice
  // on every visit.
  if (registered) return null;

  switch (pushState()) {
    case "unsupported":
      // Nothing to be done here, by us or by them. Say so and stop.
      return {
        text: "This browser cannot receive nudges — nothing will arrive here.",
        actionable: false,
      };
    case "needs-install":
      return {
        text: "Add btw to your Home Screen to get nudges →",
        actionable: true,
      };
    case "denied":
      // Semi-actionable: a permission refused once cannot be asked for again in code, but
      // the settings screen says where the browser's own switch lives.
      return {
        text: "Notifications are blocked for this site — nothing will arrive →",
        actionable: true,
      };
    case "off":
      return {
        text: "Nudges are off — nothing will arrive. Turn them on →",
        actionable: true,
      };
    case "ready":
      // Permission granted, but nothing is registered anywhere — a subscription that
      // failed, or a device forgotten from another browser.
      return {
        text: "This browser is not registered — nudges will not arrive. Register it →",
        actionable: true,
      };
  }
}

export function App() {
  const [view, go] = useRoute();

  const me = useQuery({ queryKey: qk.me, queryFn: getAuthMe, retry: false });
  const devices = useQuery({
    queryKey: qk.devices,
    queryFn: getDevices,
    enabled: me.isSuccess,
  });

  // The server never redirects an API call — a 302 to an HTML page is the least useful
  // thing a fetch can receive — so the island reads the 401 and sends somebody on itself.
  useEffect(() => {
    if (me.error instanceof ApiError && me.error.status === 401) {
      window.location.replace("/login");
    }
  }, [me.error]);

  if (me.isPending) return <Shell />;
  if (!me.isSuccess) return <Shell />;

  // Waiting for the device list rather than guessing, so the bar does not flash a reason
  // that turns out to be wrong a moment later.
  const why = devices.isSuccess
    ? silence(devices.data.devices.length > 0)
    : null;

  return (
    <Shell>
      <header className="flex items-baseline justify-between gap-4 px-5 pt-6 pb-4">
        <button
          onClick={() => go("list")}
          className="text-2xl font-semibold tracking-tight text-fg"
        >
          btw
        </button>
        <nav className="flex items-baseline gap-4">
          <button
            onClick={() => go(view === "settings" ? "list" : "settings")}
            className="text-sm text-muted underline-offset-4 hover:text-fg hover:underline"
          >
            {view === "settings" ? "back" : "settings"}
          </button>
          {/* In the masthead rather than buried in settings, which is where it was and
              where nobody found it. It decides only whether to draw the link; every route
              behind it is refused server-side by requireAdmin. */}
          {me.data.role === "admin" && (
            <a
              href="/admin"
              className="text-sm text-muted underline-offset-4 hover:text-fg hover:underline"
            >
              admin
            </a>
          )}
        </nav>
      </header>

      {/* Standing, and not dismissible. An app that looks like it is working and silently
          never nudges anybody is the failure the whole enable flow exists to prevent, and a
          banner somebody can dismiss is a failure somebody dismisses. */}
      {why && view === "list" && (
        <SilenceBar why={why} onAct={() => go("settings")} />
      )}

      {view === "list" ? <Reminders /> : <Settings />}
    </Shell>
  );
}

function SilenceBar({
  why,
  onAct,
}: {
  why: NonNullable<Silence>;
  onAct: () => void;
}) {
  const box =
    "mx-5 mb-4 block w-[calc(100%-2.5rem)] rounded-lg border px-4 py-3 text-left text-sm";

  // A statement is not a button. Drawing the dead-end case in the same accent as the
  // actionable one is what made it look tappable in the first place.
  if (!why.actionable) {
    return <p className={`${box} border-line text-muted`}>{why.text}</p>;
  }
  return (
    <button
      onClick={onAct}
      className={`${box} border-accent/40 bg-accent/10 text-accent`}
    >
      {why.text}
    </button>
  );
}

function Shell({ children }: { children?: React.ReactNode }) {
  return (
    <div className="min-h-dvh bg-bg text-fg">
      <div className="mx-auto max-w-xl pb-24">{children}</div>
    </div>
  );
}
