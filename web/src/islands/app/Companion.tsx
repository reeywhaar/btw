import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteCompanion,
  getCompanion,
  getCompanionAdvice,
  postCompanionAdviceRefresh,
  postCompanionTest,
  putCompanion,
  type Advice,
  type AdvisedReminder,
  type Companion as CompanionType,
  type CompanionEdit,
} from "@app/api/actions/companion";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Row } from "@app/components/Row";
import { Section } from "@app/components/Section";
import { CheckIcon } from "@app/components/icons/CheckIcon";
import { CrossCircleIcon } from "@app/components/icons/CrossCircleIcon";
import { WarningIcon } from "@app/components/icons/WarningIcon";
import { TextArea } from "@app/components/TextArea";
import { TextField } from "@app/components/TextField";

/**
 * The model this account has given a key for.
 *
 * Here and not on the admin page, unlike the mail relay: the key spends its owner's credit, and
 * what it is told is a description of one person's life.
 */
export function Companion() {
  const client = useQueryClient();
  const companion = useQuery({ queryKey: qk.companion, queryFn: getCompanion });
  const [editing, setEditing] = useState(false);
  const [showing, setShowing] = useState(false);

  if (!companion.isSuccess) return null;
  const c = companion.data;

  return (
    <>
      <Section
        title="Companion"
        footer={
          <Note>
            btw runs no model of its own. It puts a question to one you have a
            key for, through OpenRouter, and tells it what you write below.
          </Note>
        }
      >
        {c.configured && (
          <>
            <Field
              label="Model"
              control={
                <span className="text-sm text-muted">
                  {/* What will actually be asked, and whether it was chosen. An account that
                      picked nothing follows the default wherever it goes, and showing the
                      name alone would read as a choice it never made. */}
                  {c.model || `${c.default_model} · default`}
                </span>
              }
            />
            <Field
              label="About you"
              control={
                <span className="text-sm text-muted">
                  {c.about ? `${c.about.length} characters` : "nothing yet"}
                </span>
              }
            />
            {c.advice && <AdviceStatus advice={c.advice} />}
          </>
        )}

        <Row>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setEditing(true)}>
              {c.configured ? "Change" : "Add a key"}
            </Button>
            {c.configured && (
              <Button variant="quiet" onClick={() => setShowing(true)}>
                What it thinks
              </Button>
            )}
            {c.configured && (
              <Button
                variant="link"
                onClick={async () => {
                  await deleteCompanion();
                  await client.invalidateQueries({ queryKey: qk.companion });
                }}
              >
                Forget it
              </Button>
            )}
          </div>
        </Row>
      </Section>

      <CompanionDialog
        open={editing}
        current={c}
        onClose={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          void client.invalidateQueries({ queryKey: qk.companion });
        }}
      />
      <AdviceDialog
        open={showing}
        onClose={() => setShowing(false)}
        onRefreshed={() =>
          void client.invalidateQueries({ queryKey: qk.companion })
        }
      />
    </>
  );
}

/**
 * Whether the companion is actually doing anything — otherwise a key that stopped working and a
 * companion with little to say look identical.
 *
 * No counts: "some of them" rather than "5 of 7". See docs/api_design.md.
 */
function AdviceStatus({ advice }: { advice: Advice }) {
  // The mark and the sentence together, so neither has to be read to understand the other.
  // The icons are aria-hidden — a screen reader announcing both would say it twice.
  const { Mark, tone, said } = {
    none: {
      // Nothing has happened yet, which is neither good nor bad. A mark here would have to
      // pick one of those, and both would be wrong.
      Mark: null,
      tone: "",
      said: "It has not been asked yet.",
    },
    all: {
      Mark: CheckIcon,
      tone: "text-ok",
      said: "It has an opinion about all your reminders.",
    },
    some: {
      Mark: WarningIcon,
      tone: "text-warn",
      said: "It has an opinion about some of your reminders.",
    },
    limited: {
      Mark: WarningIcon,
      tone: "text-warn",
      said: "Its key is out of requests for now. It will try again on its own.",
    },
    failed: {
      // The refusal colour, and only here. A quota takes the caution above instead: the
      // accent meaning "this was rejected" everywhere else is what makes it worth reading
      // when it does appear.
      Mark: CrossCircleIcon,
      tone: "text-accent",
      said: "The last time it was asked, something went wrong.",
    },
  }[advice.status];

  // The moment that answers "is this current" — which is the last *attempt* when one failed,
  // and the last answer otherwise. Showing the older of the two beside a failure would read
  // as though nothing had happened since.
  const at =
    advice.status === "failed" || advice.status === "limited"
      ? advice.attempted_at
      : advice.advised_at;

  return (
    <Field
      label="Advice"
      control={<span className="text-sm text-muted">{ago(at)}</span>}
    >
      <Note>
        {Mark && (
          // Drawn at 1em and nudged onto the baseline, so it sits on the line of the sentence
          // rather than floating above it the way a fixed-size icon does.
          <Mark className={`mr-1.5 inline-block align-[-0.1em] ${tone}`} />
        )}
        {said}
        {advice.stale && advice.status !== "none" && (
          <> Something has changed since, so it will look again shortly.</>
        )}
      </Note>
      {/* The gateway's own words. A rejected key, a model with no credit and a slug that does
          not exist are three different afternoons, and only the first is worth panicking about. */}
      {advice.status === "failed" && advice.error && (
        <p className="text-sm break-words text-accent">{advice.error}</p>
      )}
    </Field>
  );
}

/**
 * Roughly how long ago, in words. Never a clock time: btw shows no schedule, and a timestamp
 * here would be the one place inviting somebody to work out when the next one is due.
 */
function ago(at: number | null): string {
  if (at === null) return "never";
  const minutes = Math.floor((Date.now() / 1000 - at) / 60);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? "" : "s"} ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} hour${hours === 1 ? "" : "s"} ago`;
  const days = Math.floor(hours / 24);
  return `${days} day${days === 1 ? "" : "s"} ago`;
}

function CompanionDialog({
  open,
  current,
  onClose,
  onSaved,
}: {
  open: boolean;
  current: CompanionType;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState<CompanionEdit>({
    api_key: "",
    model: "",
    about: "",
  });

  const save = useMutation({ mutationFn: putCompanion, onSuccess: onSaved });
  const test = useMutation({ mutationFn: postCompanionTest });
  const { reset } = test;

  // Seeded when the dialog opens rather than on every render, so typing is not overwritten
  // by the query refetching underneath.
  useEffect(() => {
    if (!open) return;
    setForm({
      api_key: "",
      // The stored value, blank included, since blank is what "follow the default" looks like.
      // Seeding it with the default's name instead would hand back a model nobody typed, and
      // the next save would pin it.
      model: current.model,
      about: current.about,
    });
    // And whatever the last press said, which was about values this dialog no longer holds.
    // reset is stable, so naming it here does not re-run the effect on every render.
    reset();
  }, [open, current, reset]);

  const set = <K extends keyof CompanionEdit>(k: K, v: CompanionEdit[K]) => {
    // A result is about the values that produced it. Leaving "it answered" standing under a
    // key that has since been retyped is the one way this button can actively mislead.
    reset();
    setForm((f) => ({ ...f, [k]: v }));
  };

  const over = form.about.length - current.about_limit;

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Companion"
      footer={
        <>
          {/* Left of the pair that commits, because it is neither of them. Button overwrites
              its own className, so the pushing-apart is done by a wrapper. */}
          <div className="mr-auto">
            <Button
              variant="quiet"
              disabled={test.isPending || (!form.api_key && !current.key_set)}
              onClick={() =>
                test.mutate({ api_key: form.api_key, model: form.model })
              }
            >
              {test.isPending ? "trying…" : "Try it"}
            </Button>
          </div>
          <Button variant="link" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={save.isPending || over > 0}
            onClick={() => save.mutate(form)}
          >
            {save.isPending ? "saving…" : "Save"}
          </Button>
        </>
      }
    >
      <TextField
        label="OpenRouter key"
        type="password"
        placeholder="sk-or-v1-…"
        autoCapitalize="none"
        autoComplete="off"
        hint={
          current.key_set
            ? "A key is stored. Leave this empty to keep it."
            : "From openrouter.ai/keys. It is stored as written and never sent back out."
        }
        value={form.api_key}
        onChange={(e) => set("api_key", e.target.value)}
      />
      <TextField
        label="Model"
        placeholder={current.default_model}
        autoCapitalize="none"
        autoComplete="off"
        hint="Leave it empty to follow the default, which is free and may change."
        value={form.model}
        onChange={(e) => set("model", e.target.value)}
      />
      <TextArea
        label="About you"
        rows={7}
        placeholder="I sleep until noon and go to bed at four. I work weekdays and do chores at the weekend."
        hint="Your hours, your habits, whether you can do two things at once. This is all the model knows about you."
        value={form.about}
        onChange={(e) => set("about", e.target.value)}
      />
      {/* Only once it is over. A counter that is always on turns a description into a form
          to fill correctly, which is the opposite of what this field wants. */}
      {over > 0 && (
        <p className="text-sm text-accent">
          {over} character{over === 1 ? "" : "s"} too many.
        </p>
      )}

      {test.isSuccess && (
        // What answered, and not what was asked for: OpenRouter falls back between providers,
        // and knowing which one replied is worth more than "it worked".
        <p
          className={
            test.data.read === "all"
              ? "text-sm text-muted"
              : "text-sm text-warn"
          }
        >
          {test.data.model} answered, {test.data.tokens} tokens.{" "}
          {test.data.read === "all"
            ? "It was asked a few made-up reminders and placed all of them."
            : test.data.read === "some"
              ? "It placed some of a few made-up reminders and not the rest."
              : "It answered nothing that could be read."}
          {test.data.shapes?.length
            ? ` It sent ${[...new Set(test.data.shapes)].join(", ")}.`
            : ""}
        </p>
      )}
      {/* The gateway's own words, not "that did not work": a rejected key, a model with no
          credit and a slug that does not exist are three different afternoons. */}
      {test.error && (
        <p className="text-sm break-words text-accent">{test.error.message}</p>
      )}

      {save.error && (
        <p className="text-sm text-accent">{save.error.message}</p>
      )}
    </Dialog>
  );
}

const dayNames = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

/** The clock time a half-hour window begins at. */
function clockAt(window: number): string {
  const minutes = window * 30;
  const hh = String(Math.floor(minutes / 60)).padStart(2, "0");
  const mm = String(minutes % 60).padStart(2, "0");
  return `${hh}:${mm}`;
}

/** Whether every half hour of the week scored the same, which is an answer saying nothing. */
function isFlat(curve: number[][]): boolean {
  const first = curve[0]?.[0];
  if (first === undefined) return true;
  return curve.every((day) => day.every((v) => v === first));
}

/**
 * What the companion currently thinks, drawn rather than listed. 336 numbers per reminder is
 * something to scroll past; the shape — evenings lifted, weekend unlike a Tuesday — is what
 * somebody actually wants, and a grid answers it at a glance.
 */
function AdviceDialog({
  open,
  onClose,
  onRefreshed,
}: {
  open: boolean;
  onClose: () => void;
  onRefreshed: () => void;
}) {
  const client = useQueryClient();

  // When the last press finished, or 0. A press spends one of a small daily quota, so the
  // button holds for a moment afterwards — long enough that leaning on it is not a way to
  // burn a day's worth, short enough not to be in the way of somebody iterating on what they
  // wrote about themselves.
  const [restingSince, setRestingSince] = useState(0);
  const [resting, setResting] = useState(false);

  useEffect(() => {
    if (restingSince === 0) return;
    setResting(true);
    const id = setTimeout(() => setResting(false), restBetweenAsks);
    return () => clearTimeout(id);
  }, [restingSince]);

  const advice = useQuery({
    queryKey: qk.advice,
    queryFn: getCompanionAdvice,
    // Only while somebody is looking. This is a diagnostic, not part of the page.
    enabled: open,
  });

  const refresh = useMutation({
    mutationFn: postCompanionAdviceRefresh,
    onSuccess: (fresh) => {
      // Written straight into the cache rather than refetched: the reply *is* the answer, and
      // asking again for what was just returned is a round trip for nothing.
      client.setQueryData(qk.advice, fresh);
      setRestingSince(Date.now());
      onRefreshed();
    },
  });

  const failed = advice.data?.error ?? "";

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="What it thinks"
      footer={
        <>
          <Button variant="link" onClick={onClose}>
            Close
          </Button>
          <Button
            disabled={refresh.isPending || resting}
            onClick={() => refresh.mutate()}
          >
            {refresh.isPending
              ? "asking…"
              : resting
                ? "just asked"
                : "Ask again"}
          </Button>
        </>
      }
    >
      {refresh.isPending && (
        // Said while it happens rather than after: a model takes anywhere from seconds to a
        // couple of minutes, and a button that went quiet for that long reads as broken.
        <Note>
          Asking your companion. This takes a moment — the drawings below change
          when it answers.
        </Note>
      )}
      {failed !== "" && !refresh.isPending && (
        <p className="text-sm break-words text-accent">{failed}</p>
      )}
      {refresh.error && (
        <p className="text-sm break-words text-accent">
          {refresh.error.message}
        </p>
      )}

      {advice.isSuccess && advice.data.reminders.length === 0 && (
        <Note>
          Nothing written down yet, so there is nothing to think about.
        </Note>
      )}
      {advice.isSuccess &&
        advice.data.reminders.map((r) => <AdviceRow key={r.id} reminder={r} />)}
      {advice.error && (
        <p className="text-sm text-accent">{advice.error.message}</p>
      )}
    </Dialog>
  );
}

/**
 * How long the button rests after a press.
 *
 * A press spends one of a small daily quota. The server has its own ceiling — this is the one
 * that stops somebody reaching it by accident, on the screen where reaching it is easiest.
 */
const restBetweenAsks = 20 * 1000;

function AdviceRow({ reminder }: { reminder: AdvisedReminder }) {
  return (
    <div className="flex flex-col gap-1.5 border-t border-line pt-4 first:border-0 first:pt-0">
      <span className="text-sm text-fg">{reminder.text}</span>

      {!reminder.advised && (
        <span className="text-xs text-faint">Nothing said about this yet.</span>
      )}

      {reminder.categories && reminder.categories.length > 0 && (
        <span className="text-xs text-faint">
          {reminder.categories.join(", ")}
          {reminder.exclusive ? " · wants full attention" : ""}
        </span>
      )}

      {reminder.curve && isFlat(reminder.curve) && (
        // A flat curve is data-shaped and says nothing. Drawn, it is a uniform grey band that
        // looks exactly like an answer, so it is worth saying out loud that it is not one.
        <span className="text-xs text-faint">
          No opinion about when — every hour scored the same.
        </span>
      )}
      {reminder.curve && !isFlat(reminder.curve) && (
        <Week curve={reminder.curve} />
      )}

      {reminder.advised && !reminder.curve && (
        <span className="text-xs text-faint">
          It answered, but not in a shape that could be read
          {reminder.shape ? ` — it sent ${reminder.shape}` : ""}.
        </span>
      )}
    </div>
  );
}

/**
 * A week, one row per day, drawn as half-hour bars against the rules that make a level readable.
 *
 * Gapped bars rather than a filled outline: the answer is forty-eight buckets, not a continuous
 * function. Divs rather than SVG, because a chart that fills its width needs
 * preserveAspectRatio="none", which turns a one-pixel gap into a variable one.
 */
function Week({ curve }: { curve: number[][] }) {
  return (
    <div className="flex flex-col gap-1">
      {curve.map((day, i) => (
        <div key={i} className="flex items-center gap-2">
          <span className="w-7 shrink-0 text-[10px] text-faint">
            {dayNames[i]}
          </span>
          <div
            className="relative h-8 flex-1 overflow-hidden rounded-sm bg-plot"
            role="img"
            aria-label={`${dayNames[i]}, half-hourly`}
          >
            {/* Six-hourly, so a shape can be placed against a time without counting bars. */}
            {[12, 24, 36].map((x) => (
              <div
                key={x}
                className="absolute inset-y-0 w-px bg-fg/10"
                style={{ left: `${(x / day.length) * 100}%` }}
              />
            ))}
            {/* The rule everything is read against: above it is better than usual. */}
            <div className="absolute inset-x-0 top-1/2 border-t border-dashed border-fg/30" />

            <div className="absolute inset-0 flex items-end gap-px px-px">
              {day.map((v, x) => (
                <div
                  key={x}
                  className="flex-1 rounded-t-[2px] bg-plot-ink"
                  style={{ height: `${Math.min(Math.max(v, 0), 1) * 100}%` }}
                  // The exact value, since a height can be compared but not read. A native
                  // tooltip costs one attribute and works wherever a pointer does.
                  title={`${dayNames[i]} ${clockAt(x)} · ${v.toFixed(1)}`}
                />
              ))}
            </div>
          </div>
        </div>
      ))}
      <div className="flex pl-9 text-[10px] text-faint">
        <span className="flex-1 text-left">00:00</span>
        <span className="flex-1 text-center">12:00</span>
        <span className="flex-1 text-right">24:00</span>
      </div>
    </div>
  );
}
