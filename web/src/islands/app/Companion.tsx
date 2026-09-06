import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteCompanion,
  getCompanion,
  postCompanionTest,
  putCompanion,
  type Advice,
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
import { TextArea } from "@app/components/TextArea";
import { TextField } from "@app/components/TextField";

/**
 * The model this account has given a key for.
 *
 * Here and not on the admin page, unlike the mail relay: a relay is the instance's and a
 * companion is not. The key spends its owner's credit, and what it is told is a description
 * of one person's life that nobody else on the instance should be scheduled against.
 */
export function Companion() {
  const client = useQueryClient();
  const companion = useQuery({ queryKey: qk.companion, queryFn: getCompanion });
  const [editing, setEditing] = useState(false);

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
              control={<span className="text-sm text-muted">{c.model}</span>}
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
    </>
  );
}

/**
 * Whether the companion is actually doing anything.
 *
 * Without this the two states somebody most needs to tell apart look identical: a key that
 * stopped working and a companion that simply has little to say. It was the last gap in the
 * feature — the reason a failure was recorded at all was so it could be shown.
 *
 * No counts, deliberately: "some of them" rather than "5 of 7". See docs/api_design.md.
 */
function AdviceStatus({ advice }: { advice: Advice }) {
  const said = {
    none: "It has not been asked yet.",
    all: "It has an opinion about all your reminders.",
    some: "It has an opinion about some of your reminders.",
    limited:
      "Its key is out of requests for now. It will try again on its own.",
    failed: "The last time it was asked, something went wrong.",
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
 * Roughly how long ago, in words.
 *
 * Rough on purpose, and never a clock time. btw does not show when anything is scheduled, and
 * a precise timestamp here would be the one place in the product inviting somebody to work out
 * when the next one is due.
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
      model: current.configured ? current.model : "",
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
        hint="Empty means the default, which is free."
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
        <p className="text-sm text-muted">
          {test.data.model} answered, {test.data.tokens} tokens.
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
