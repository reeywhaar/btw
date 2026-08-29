import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteDevicesById,
  getDevices,
  getRhythm,
  patchRhythm,
  postLogout,
  postNudge,
} from "@app/api/actions";
import { qk } from "@app/api/keys";
import { enable, installed, isIOS, pushState, type PushState } from "@app/push";

export function Settings() {
  return (
    <main className="space-y-10 px-5">
      <ThisBrowser />
      <Devices />
      <RhythmPanel />
      <Account />
    </main>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section>
      <h2 className="pb-3 text-xs font-medium tracking-widest text-faint uppercase">
        {title}
      </h2>
      {children}
    </section>
  );
}

/**
 * What this browser can do about nudges.
 *
 * Split from Devices below on purpose. Everything here is a fact about the browser you are
 * holding — whether it has a Push API, whether permission was granted, whether it needs
 * installing first. Everything there is a fact about the account.
 *
 * They used to be one block, and the device list and the test button were nested inside the
 * "permission is granted" branch. That meant opening btw on a laptop that cannot receive
 * push hid the phone that can, along with the only button that could reach it — the state of
 * the browser in front of you deciding what you may know about a device somewhere else.
 */
function ThisBrowser() {
  const [state, setState] = useState<PushState>(() => pushState());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const client = useQueryClient();

  const devices = useQuery({ queryKey: qk.devices, queryFn: getDevices });
  const registered = devices.isSuccess && devices.data.devices.length > 0;

  async function turnOn() {
    setBusy(true);
    setError("");
    try {
      // Asked from this press and never from page load — on iOS strictly, and it is good
      // manners everywhere. A permission refused once cannot be asked for again in code.
      setState(await enable());
      await client.invalidateQueries({ queryKey: qk.devices });
    } catch (e) {
      setError(e instanceof Error ? e.message : "that did not work");
    } finally {
      setBusy(false);
    }
  }

  const button = (label: string) => (
    <button
      onClick={turnOn}
      disabled={busy}
      className="rounded-lg bg-fg px-4 py-2.5 font-medium text-bg disabled:opacity-50"
    >
      {busy ? "asking…" : label}
    </button>
  );

  return (
    <Section title="This browser">
      <div className="space-y-4">
        {state === "needs-install" && <InstallGate />}

        {state === "unsupported" && (
          // Naming the missing capability rather than listing browsers, because the list
          // would be every mainstream browser of the last few years — which tells somebody
          // sitting in front of one that cannot do it exactly nothing.
          <p className="text-sm text-muted">
            This browser has no Push API, so nudges cannot reach it and nothing
            will arrive here. Everything else still works — what you write down
            will be waiting in whatever you next open btw in.
          </p>
        )}

        {state === "denied" && (
          <p className="text-sm text-muted">
            Notifications are blocked for this site. A permission refused once
            cannot be asked for again — turn it back on in your browser&apos;s
            settings for this site, then reload.
          </p>
        )}

        {state === "off" && (
          <>
            <p className="text-sm text-muted">
              A few times a day, at hours nobody picked, one of the things you
              have written down will arrive. You can do it, drop it, or ignore
              it — ignoring it costs nothing.
            </p>
            {button("Turn on nudges")}
          </>
        )}

        {state === "ready" && !registered && (
          <>
            <p className="text-sm text-muted">
              Permission is granted but this browser is not registered. Press
              once more.
            </p>
            {button("Register this device")}
          </>
        )}

        {state === "ready" && registered && (
          <p className="text-sm text-muted">
            This browser will receive nudges.
          </p>
        )}

        {error && <p className="text-sm text-accent">{error}</p>}
      </div>
    </Section>
  );
}

/**
 * The devices this account can be reached on, and the button that proves it.
 *
 * Shown whenever there is at least one, whatever the browser in front of you can do. A
 * laptop with no Push API is still the place somebody manages their phone from — and is
 * often the more comfortable place to do it.
 */
function Devices() {
  const client = useQueryClient();
  const devices = useQuery({ queryKey: qk.devices, queryFn: getDevices });
  const test = useMutation({ mutationFn: postNudge });

  if (!devices.isSuccess || devices.data.devices.length === 0) return null;

  return (
    <Section title="Devices">
      <div className="space-y-4">
        <ul className="space-y-2 text-sm">
          {devices.data.devices.map((d) => (
            <li key={d.id} className="flex items-center justify-between gap-3">
              <span className="text-muted">{d.label || "a browser"}</span>
              <button
                onClick={async () => {
                  await deleteDevicesById(d.id);
                  await client.invalidateQueries({ queryKey: qk.devices });
                }}
                className="text-faint underline-offset-4 hover:text-accent hover:underline"
              >
                forget
              </button>
            </li>
          ))}
        </ul>

        <div>
          {/* The button that proves the chain — permission, subscription, VAPID,
              encryption, service worker, notification — in one press. It stays in the
              product, because setting up a new phone asks the same question.

              It sends to every device on the account, not to this one, which is what makes
              it useful from a browser that cannot receive anything itself. */}
          <button
            onClick={() => test.mutate()}
            disabled={test.isPending}
            className="rounded-lg border border-line px-4 py-2.5 text-sm text-muted hover:border-line-strong hover:text-fg disabled:opacity-50"
          >
            {test.isPending ? "sending…" : "Send one now"}
          </button>
          {test.isSuccess && !test.data.sent && (
            <p className="pt-2 text-sm text-faint">
              Nothing to send: everything is finished, or was raised too
              recently.
            </p>
          )}
          {test.isSuccess && test.data.sent && (
            <p className="pt-2 text-sm text-faint">
              Sent. It should be on its way.
            </p>
          )}
          {test.error && (
            <p className="pt-2 text-sm text-accent">{test.error.message}</p>
          )}
        </div>
      </div>
    </Section>
  );
}

/**
 * The install gate.
 *
 * Safari delivers Web Push only to a web app added to the Home Screen. Offering a button
 * that cannot work there is how somebody taps Enable, sees nothing happen, and never comes
 * back — the likeliest way this product fails on the device it is for.
 */
function InstallGate() {
  return (
    <div className="space-y-3 rounded-lg border border-accent/30 bg-accent/5 p-4">
      <p className="text-sm text-fg">
        On iPhone and iPad, notifications only work once btw is on your Home
        Screen.
      </p>
      <ol className="list-inside list-decimal space-y-1 text-sm text-muted">
        <li>Tap the Share button in Safari</li>
        <li>Choose “Add to Home Screen”</li>
        <li>Open btw from there, and come back to this screen</li>
      </ol>
      {!isIOS() && !installed() && (
        <p className="text-sm text-faint">
          Elsewhere, installing is optional — it only means a nudge opens btw
          rather than a browser window.
        </p>
      )}
    </div>
  );
}

function RhythmPanel() {
  const client = useQueryClient();
  const rhythm = useQuery({ queryKey: qk.rhythm, queryFn: getRhythm });
  const save = useMutation({
    mutationFn: patchRhythm,
    onSuccess: () => client.invalidateQueries({ queryKey: qk.rhythm }),
  });

  if (!rhythm.isSuccess) return null;
  const r = rhythm.data;

  // Offered once, and only when it disagrees: a timezone somebody has already confirmed is
  // not something to ask about on every visit.
  const here = Intl.DateTimeFormat().resolvedOptions().timeZone;

  return (
    <Section title="Rhythm">
      <div className="space-y-4 text-sm">
        <label className="flex items-center justify-between gap-4">
          <span className="text-muted">A day holds</span>
          <select
            value={r.budget}
            onChange={(e) => save.mutate({ budget: Number(e.target.value) })}
            className="rounded-md border border-line bg-surface px-3 py-2 text-fg"
          >
            {Array.from(
              { length: Math.max(r.max_budget, r.budget) + 1 },
              (_, n) => (
                <option key={n} value={n}>
                  {n === 0 ? "none" : n === 1 ? "1 nudge" : `${n} nudges`}
                </option>
              ),
            )}
          </select>
        </label>

        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            {/* The checkbox is its own label. Wrapping the selects in it too would mean
                every attempt to change an hour toggled the box instead. */}
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={r.window_enabled}
                onChange={(e) =>
                  save.mutate({ window_enabled: e.target.checked })
                }
                className="size-4 accent-accent"
              />
              <span className="text-muted">Only between</span>
            </label>
            <Hour
              value={r.wake_minute}
              disabled={!r.window_enabled}
              onChange={(v) => save.mutate({ wake_minute: v })}
            />
            <span className="text-faint">and</span>
            <Hour
              value={r.sleep_minute}
              disabled={!r.window_enabled}
              onChange={(v) => save.mutate({ sleep_minute: v })}
            />
          </div>

          {!r.window_enabled && (
            // Said out loud, because unchecking a box is not an obvious way to ask for a
            // notification at four in the morning, and that is what it does.
            <p className="text-faint">
              A nudge can arrive at any hour, including while you are asleep.
            </p>
          )}
        </div>

        {r.timezone !== here && (
          <button
            onClick={() => save.mutate({ timezone: here })}
            className="text-faint underline-offset-4 hover:text-fg hover:underline"
          >
            Your hours are set in {r.timezone}. Use {here} instead?
          </button>
        )}

        {save.error && <p className="text-accent">{save.error.message}</p>}

        {/* Deliberately nowhere: when the next one is due. A person who can see that it is
            at 14:32 is a person waiting for 14:32, and the surprise is the mechanism. */}
        <p className="pt-2 text-faint">
          When exactly is not shown, and is not knowable. That is the point of
          it.
        </p>
      </div>
    </Section>
  );
}

function Hour({
  value,
  onChange,
  disabled,
}: {
  value: number;
  onChange: (v: number) => void;
  disabled?: boolean;
}) {
  return (
    <select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(Number(e.target.value))}
      className="rounded-md border border-line bg-surface px-3 py-2 text-fg disabled:text-faint disabled:opacity-50"
    >
      {Array.from({ length: 25 }, (_, h) => (
        <option key={h} value={h * 60}>
          {String(h).padStart(2, "0")}:00
        </option>
      ))}
    </select>
  );
}

function Account() {
  return (
    <Section title="Account">
      <button
        onClick={async () => {
          await postLogout();
          window.location.replace("/login");
        }}
        className="text-sm text-muted underline-offset-4 hover:text-fg hover:underline"
      >
        Sign out
      </button>
    </Section>
  );
}
