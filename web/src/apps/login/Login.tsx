import { useEffect, useState } from "react";

import {
  getInvitesByToken,
  postInvitesByTokenAccept,
  postLogin,
} from "@app/api/actions";

/**
 * Two pages in one island: signing in, and accepting an invitation.
 *
 * The path decides, read directly rather than through a router — this island has exactly
 * two states and no sub-navigation, and a router for that is a dependency that earns
 * nothing.
 */
export function Login() {
  const path = window.location.pathname;
  const token = path.startsWith("/invite/")
    ? decodeURIComponent(path.slice("/invite/".length))
    : "";
  return token ? <Accept token={token} /> : <SignIn />;
}

function Frame({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg px-5 text-fg">
      <div className="w-full max-w-sm space-y-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">btw</h1>
          <p className="pt-1 text-sm text-faint">{title}</p>
        </div>
        {children}
      </div>
    </div>
  );
}

const field =
  "w-full rounded-lg border border-line bg-surface px-4 py-3 text-fg placeholder:text-faint focus:border-accent/60 focus:outline-none";
const button =
  "w-full rounded-lg bg-fg px-4 py-3 font-medium text-bg disabled:opacity-50";

function SignIn() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  return (
    <Frame title="A place to put a thought down and stop carrying it">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            await postLogin(username, password);
            window.location.replace("/");
          } catch (err) {
            setError(err instanceof Error ? err.message : "that did not work");
          } finally {
            setBusy(false);
          }
        }}
      >
        <input
          className={field}
          placeholder="username"
          autoComplete="username"
          autoCapitalize="none"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <input
          className={field}
          type="password"
          placeholder="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <button className={button} disabled={busy}>
          {busy ? "…" : "Sign in"}
        </button>
        {error && <p className="text-sm text-accent">{error}</p>}
      </form>
    </Frame>
  );
}

function Accept({ token }: { token: string }) {
  const [valid, setValid] = useState<boolean | null>(null);
  const [problem, setProblem] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // Asked before anybody types a password into it. The endpoint reveals nothing but the
  // link's own validity.
  useEffect(() => {
    getInvitesByToken(token)
      .then(() => setValid(true))
      .catch((err: unknown) => {
        setValid(false);
        setProblem(
          err instanceof Error ? err.message : "that link is not valid",
        );
      });
  }, [token]);

  if (valid === null) return <Frame title="checking that link…">{null}</Frame>;
  if (!valid) return <Frame title={problem}>{null}</Frame>;

  return (
    <Frame title="Choose a name and a password">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            await postInvitesByTokenAccept(token, username, password);
            // Signed in already: accepting an invitation and then being shown a login form
            // is asking somebody to prove something they just proved.
            window.location.replace("/");
          } catch (err) {
            setError(err instanceof Error ? err.message : "that did not work");
          } finally {
            setBusy(false);
          }
        }}
      >
        <input
          className={field}
          placeholder="username"
          autoComplete="username"
          autoCapitalize="none"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <input
          className={field}
          type="password"
          placeholder="password — at least 8 characters"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <button className={button} disabled={busy}>
          {busy ? "…" : "Create the account"}
        </button>
        {error && <p className="text-sm text-accent">{error}</p>}
      </form>
    </Frame>
  );
}
