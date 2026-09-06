import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteCompanion,
  getCompanion,
  postCompanionTest,
  putCompanion,
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
