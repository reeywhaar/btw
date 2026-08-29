import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";

import { getAuthMe } from "@app/api/actions/auth";
import { qk } from "@app/api/keys";
import { ApiError } from "@app/api/transport";
import { Relay } from "@app/islands/admin/Relay";

/**
 * The administrator's island.
 *
 * A shell of its own rather than a route inside the app, because almost nobody is an
 * administrator and this is code everybody else would otherwise be downloading.
 *
 * The check here decides only what to draw. Every route it calls is refused server-side by
 * requireAdmin, which is where the decision is actually made — a check inside a bundle
 * anybody can read is a hint, not a gate.
 */
export function Admin() {
  const me = useQuery({ queryKey: qk.me, queryFn: getAuthMe, retry: false });

  useEffect(() => {
    if (me.error instanceof ApiError && me.error.status === 401) {
      window.location.replace("/login");
    }
  }, [me.error]);

  return (
    <div className="min-h-dvh bg-bg text-fg">
      <div className="mx-auto max-w-xl pb-24">
        <header className="flex items-baseline justify-between gap-4 px-5 pt-6 pb-4">
          <a href="/" className="text-2xl font-semibold tracking-tight text-fg">
            btw
          </a>
          <a
            href="/"
            className="text-sm text-muted underline-offset-4 hover:text-fg hover:underline"
          >
            back
          </a>
        </header>

        {me.isSuccess && me.data.role !== "admin" && (
          <p className="px-5 text-sm text-muted">
            This page is for administrators.
          </p>
        )}

        {me.isSuccess && me.data.role === "admin" && (
          <main className="space-y-8 px-4">
            <Relay />
          </main>
        )}
      </div>
    </div>
  );
}
