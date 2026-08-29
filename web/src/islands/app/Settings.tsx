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

  // Offered once, and only when it disagrees: a timezone somebody has already confirmed is
  // not something to ask about on every visit.
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
            {/* Able to render the value it already holds. Switching the window back on can
                leave a budget above what that window takes; the save is refused with a
                sentence, and a select whose value is missing from its options would go
                blank before anybody read the sentence. */}
            {Array.from(
              { length: Math.max(r.max_budget, r.budget) + 1 },
              (_, n) => (
                <option key={n} value={n}>
                  {n === 0 ? "none" : n === 1 ? "1 nudge" : `${n} nudges`}
                </option>
              ),
            )}
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

        {!r.window_enabled && (
          // Said out loud, because unticking a box is not an obvious way to ask for a
          // notification at four in the morning, and that is what it does.
          <Note>
            A nudge can arrive at any hour, including while you are asleep.
          </Note>
        )}
      </Field>

      {r.timezone !== here && (
        <Field
          label={`Your hours are set in ${r.timezone}`}
          control={
            <Button
              variant="link"
              onClick={() => save.mutate({ timezone: here })}
            >
              use {here}
            </Button>
          }
        />
      )}

      {save.error && (
        <Row>
          <p className="text-sm text-accent">{save.error.message}</p>
        </Row>
      )}
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
    <Select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(Number(e.target.value))}
    >
      {Array.from({ length: 25 }, (_, h) => (
        <option key={h} value={h * 60}>
          {String(h).padStart(2, "0")}:00
        </option>
      ))}
    </Select>
  );
}
