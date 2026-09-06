import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { deleteDevicesById, getDevices } from "@app/api/actions/devices";
import { postNudges } from "@app/api/actions/nudges";
import { getRhythm, patchRhythm } from "@app/api/actions/rhythm";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Check } from "@app/components/Check";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Row } from "@app/components/Row";
import { Section } from "@app/components/Section";
import { Select } from "@app/components/Select";
import { Warning } from "@app/components/Warning";
import { Companion } from "@app/islands/app/Companion";
import {
  enable,
  installed,
  isIOS,
  pushState,
  storedClientID,
  type PushState,
} from "@app/push";

export function Settings() {
  return (
    <main className="space-y-8 px-4">
      <ThisBrowser />
      <Devices />
      <RhythmPanel />
      <Companion />
    </main>
  );
}

/**
 * What this browser can do about nudges.
 *
 * Split from Devices below on purpose. Everything here is a fact about the browser you are
 * holding — whether it has a Push API, whether permission was granted, whether it needs
 * installing first. Everything there is a fact about the account.
 *
 * They used to be one block, with the device list and the test button nested inside the
 * "permission is granted" branch. That meant opening btw on a laptop that cannot receive
 * push hid the phone that can, along with the only button that could reach it.
 */
function ThisBrowser() {
  const [state, setState] = useState<PushState>(() => pushState());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const client = useQueryClient();

  const devices = useQuery({ queryKey: qk.devices, queryFn: getDevices });

  // Whether *this* browser is registered, not whether any device is.
  //
  // It was `devices.length > 0`, which is a different question and gave the wrong answer to
  // this one: a laptop that had never registered was told "this browser will receive
  // nudges" because a phone had, and was offered no way to register. It also hid the button
  // from a browser whose row predates the client id, which is exactly the browser that
  // needs to press it — doing so adopts its existing row rather than adding one.
  const mine = storedClientID();
  const registered =
    devices.isSuccess &&
    devices.data.devices.some((d) => d.client_id && d.client_id === mine);

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

  const enableButton = (label: string) => (
    <Row>
      <Button onClick={turnOn} disabled={busy}>
        {busy ? "asking…" : label}
      </Button>
    </Row>
  );

  return (
    <Section title="This browser">
      {state === "needs-install" && <InstallGate />}

      {state === "unsupported" && (
        // Naming the missing capability rather than listing browsers, because the list
        // would be every mainstream browser of the last few years — which tells somebody
        // sitting in front of one that cannot do it exactly nothing.
        <Row>
          <Warning>
            This browser has no Push API, so nudges cannot reach it and nothing
            will arrive here. Everything else still works — what you write down
            will be waiting in whatever you next open btw in.
          </Warning>
        </Row>
      )}

      {state === "denied" && (
        <Row>
          <Warning>
            Notifications are blocked for this site. A permission refused once
            cannot be asked for again — turn it back on in your browser&apos;s
            settings for this site, then reload.
          </Warning>
        </Row>
      )}

      {state === "off" && (
        <>
          <Row>
            <p className="text-sm text-muted">
              A few times a day, at hours nobody picked, one of the things you
              have written down will arrive. You can do it, drop it, or ignore
              it — ignoring it costs nothing.
            </p>
          </Row>
          {enableButton("Turn on nudges")}
        </>
      )}

      {state === "ready" && !registered && (
        <>
          <Row>
            <p className="text-sm text-muted">
              Permission is granted but this browser is not registered.
            </p>
          </Row>
          {enableButton("Register this device")}
        </>
      )}

      {state === "ready" && registered && (
        <Row>
          <p className="text-sm text-muted">
            This browser will receive nudges.
          </p>
        </Row>
      )}

      {error && (
        <Row>
          <p className="text-sm text-accent">{error}</p>
        </Row>
      )}
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
  const test = useMutation({ mutationFn: postNudges });
  const mine = storedClientID();

  if (!devices.isSuccess || devices.data.devices.length === 0) return null;

  // Every device gets its own copy of every nudge, so a row that is not this browser and
  // is not a phone somebody recognises is a subscription that rotated out from under them
  // — and is why one press can arrive twice.
  const strangers = devices.data.devices.filter(
    (d) => !d.client_id || d.client_id !== mine,
  );

  return (
    <Section title="Devices">
      {devices.data.devices.map((d) => (
        <Field
          key={d.id}
          label={
            d.client_id && d.client_id === mine
              ? `${d.label || "a browser"} — this one`
              : d.label || "a browser"
          }
          control={
            <button
              onClick={async () => {
                await deleteDevicesById(d.id);
                await client.invalidateQueries({ queryKey: qk.devices });
              }}
              className="text-sm text-faint underline-offset-4 hover:text-accent hover:underline"
            >
              forget
            </button>
          }
        />
      ))}

      {strangers.length > 0 && (
        <Row>
          <Note>
            {strangers.length === 1
              ? "One other device"
              : `${strangers.length} other devices`}{" "}
            will receive every nudge too. Forget any you do not recognise — each
            one is a separate copy of the same reminder.
          </Note>
        </Row>
      )}

      <Row>
        {/* The button that proves the chain — permission, subscription, VAPID, encryption,
            service worker, notification — in one press. It stays in the product, because
            setting up a new phone asks the same question.

            It sends to every device on the account, not to this one, which is what makes it
            useful from a browser that can receive nothing itself. */}
        <Button
          variant="quiet"
          onClick={() => test.mutate()}
          disabled={test.isPending}
        >
          {test.isPending ? "sending…" : "Send one now"}
        </Button>
        {test.isSuccess && test.data.outcome === "nothing" && (
          <div className="pt-2">
            <Note>
              Nothing to send: everything you have written down is finished.
            </Note>
          </div>
        )}
        {test.isSuccess && test.data.outcome === "undelivered" && (
          <div className="pt-2">
            {/* A different problem entirely, and it used to share the sentence above. One
                is an empty list; this is a device that did not take it. */}
            <Warning>
              A reminder was picked, but none of your devices took it. Try
              forgetting the device and turning nudges on again.
            </Warning>
          </div>
        )}
        {test.isSuccess && test.data.sent && (
          <div className="pt-2">
            <Note>
              {test.data.delivered === 1
                ? "Sent to one device."
                : `Sent to ${test.data.delivered} devices — each shows its own notification.`}
            </Note>
          </div>
        )}
        {test.error && (
          <p className="pt-2 text-sm text-accent">{test.error.message}</p>
        )}
      </Row>
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
    <Row>
      <p className="text-sm text-fg">
        On iPhone and iPad, notifications only work once btw is on your Home
        Screen.
      </p>
      <ol className="list-inside list-decimal space-y-1 pt-3 text-sm text-muted">
        <li>Tap the Share button in Safari</li>
        <li>Choose “Add to Home Screen”</li>
        <li>Open btw from there, and come back to this screen</li>
      </ol>
      {!isIOS() && !installed() && (
        <div className="pt-3">
          <Note>
            Elsewhere, installing is optional — it only means a nudge opens btw
            rather than a browser window.
          </Note>
        </div>
      )}
    </Row>
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

  // What this browser thinks it is in. The stored zone is shown either way; this only decides
  // whether there is a correction to offer beside it.
  const here = Intl.DateTimeFormat().resolvedOptions().timeZone;

  return (
    <Section
      title="Rhythm"
      footer={
        // Deliberately nowhere: when the next one is due. A person who can see that it is
        // at 14:32 is a person waiting for 14:32, and the surprise is the mechanism.
        <Note>
          When exactly is not shown, and is not knowable. That is the point of
          it.
        </Note>
      }
    >
      <Field
        label="A day holds"
        control={
          <Select
            value={r.budget}
            onChange={(e) => save.mutate({ budget: Number(e.target.value) })}
          >
            {/* One upwards. Zero was offered as "none", which is a way of switching nudges
                off hidden inside a count of them.

                The range still stretches to hold a stored value outside it, so a budget
                saved before the ceiling moved still renders rather than leaving the control
                blank. */}
            {Array.from(
              {
                length:
                  Math.max(r.max_budget, r.budget) - Math.min(1, r.budget) + 1,
              },
              (_, i) => Math.min(1, r.budget) + i,
            ).map((n) => (
              <option key={n} value={n}>
                {n === 0 ? "none" : n === 1 ? "1 nudge" : `${n} nudges`}
              </option>
            ))}
          </Select>
        }
      />

      <Field
        label="Only at certain hours"
        control={
          <Check
            checked={r.window_enabled}
            onChange={(v) => save.mutate({ window_enabled: v })}
          />
        }
      >
        <div className="flex items-center gap-2 text-sm">
          <span className="text-faint">from</span>
          <Hour
            value={r.wake_minute}
            disabled={!r.window_enabled}
            onChange={(v) => save.mutate({ wake_minute: v })}
          />
          <span className="text-faint">to</span>
          <Hour
            value={r.sleep_minute}
            disabled={!r.window_enabled}
            onChange={(v) => save.mutate({ sleep_minute: v })}
          />
        </div>

        {/* Three things to say and only one of them at a time. A window that ends before it
            starts is the one somebody is most likely to think they have mistyped, so it says
            back what it heard. */}
        {!r.window_enabled || r.wake_minute === r.sleep_minute ? (
          // Said out loud, because neither unticking a box nor setting both ends alike is an
          // obvious way to ask for a notification at four in the morning, and both do.
          <Note>
            A nudge can arrive at any hour, including while you are asleep.
          </Note>
        ) : r.sleep_minute < r.wake_minute ? (
          <Note>
            Through midnight: {hhmm(r.wake_minute)} until {hhmm(r.sleep_minute)}{" "}
            the next morning.
          </Note>
        ) : null}
      </Field>

      <Field
        label="Arrive quietly"
        control={
          <Check
            checked={r.silent}
            onChange={(v) => save.mutate({ silent: v })}
          />
        }
      >
        {r.silent && (
          <Note>
            A nudge will show without a sound. It is still a notification — it
            simply does not announce itself.
          </Note>
        )}
      </Field>

      {/* Shown always, not only when it disagrees. It was the second, and a zone nobody had
          corrected was then invisible — while it decides both which hours a nudge may arrive
          in and which hours the companion's advice is compared against. The second of those
          applies even with the window off, so a wrong zone is worth being able to see rather
          than only worth being offered a fix for. */}
      <Field
        label="Timezone"
        control={
          <div className="flex items-center gap-3">
            <span className="text-sm text-muted">{r.timezone}</span>
            {r.timezone !== here && (
              <Button
                variant="link"
                onClick={() => save.mutate({ timezone: here })}
              >
                use {here}
              </Button>
            )}
          </div>
        }
      />

      {save.error && (
        <Row>
          <p className="text-sm text-accent">{save.error.message}</p>
        </Row>
      )}
    </Section>
  );
}

/** An hour of the day as somebody wrote it, from minutes since midnight. */
function hhmm(minute: number): string {
  return `${String(Math.floor(minute / 60)).padStart(2, "0")}:${String(minute % 60).padStart(2, "0")}`;
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
    <Select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(Number(e.target.value))}
    >
      {/* Twenty-four, not twenty-five. Midnight had both names before, and 24:00 as a start
          is an hour no minute of the day is ever past — the window may now simply wrap, which
          is what somebody choosing it actually meant. */}
      {Array.from({ length: 24 }, (_, h) => (
        <option key={h} value={h * 60}>
          {String(h).padStart(2, "0")}:00
        </option>
      ))}
    </Select>
  );
}
