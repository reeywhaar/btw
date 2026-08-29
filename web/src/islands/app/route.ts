import { useCallback, useEffect, useState } from "react";

/**
 * The places this island has, addressed by URL.
 *
 * The History API directly rather than react-router: three routes and no parameters is less
 * code than the configuration a router needs, and the whole of it is below. When a route
 * arrives with a parameter in it, that is the moment the dependency earns its place.
 *
 * Using the URL at all is not a nicety. An installed web app has no address bar and no
 * back button of its own, so the *system* back gesture is the only way out of a screen —
 * and a screen that is a useState rather than a route answers that gesture by closing the
 * application.
 */
export type Route = "list" | "settings" | "account";

const PATHS: Record<Route, string> = {
  list: "/",
  settings: "/settings",
  account: "/account",
};

function routeFor(pathname: string): Route {
  if (pathname.startsWith("/settings")) return "settings";
  if (pathname.startsWith("/account")) return "account";
  return "list";
}

/** Marks the entries this island pushed, so going back can tell its own history from the
 *  history of whatever the browser was showing before btw. */
type State = { btw?: true };

export function useRoute(): [Route, (to: Route) => void] {
  const [route, setRoute] = useState<Route>(() =>
    routeFor(window.location.pathname),
  );

  useEffect(() => {
    const onPop = () => setRoute(routeFor(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const go = useCallback((to: Route) => {
    if (routeFor(window.location.pathname) === to) return;

    // Returning to a screen we pushed from goes *back* rather than forward. Otherwise
    // opening and closing settings four times leaves four entries for the system gesture
    // to walk through before it can leave the app.
    const pushed = (window.history.state as State | null)?.btw === true;
    if (to === "list" && pushed) {
      window.history.back();
      return; // popstate sets the route.
    }

    window.history.pushState({ btw: true } satisfies State, "", PATHS[to]);
    setRoute(to);
  }, []);

  return [route, go];
}
