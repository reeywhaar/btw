import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  getAdminDefaultModel,
  putAdminDefaultModel,
} from "@app/api/actions/admin";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Row } from "@app/components/Row";
import { Section } from "@app/components/Section";
import { TextField } from "@app/components/TextField";

/**
 * The model an account gets without naming one.
 *
 * Instance-wide and an administrator's, unlike the key each account brings. It exists because
 * OpenRouter retires slugs: without it, the day the compiled-in model goes, every account that
 * never chose one breaks at once and the only fix is a new build.
 */
export function Companion() {
  const client = useQueryClient();
  const model = useQuery({
    queryKey: qk.defaultModel,
    queryFn: getAdminDefaultModel,
  });
  const [editing, setEditing] = useState(false);

  if (!model.isSuccess) return null;
  const m = model.data;

  return (
    <>
      <Section
        title="Companion"
        footer={
          <Note>
            Anyone who has typed a model of their own keeps it. Nothing is
            checked here — the instance has no key to check it with, and the
            first companion to use it reports what the gateway said.
          </Note>
        }
      >
        <Field
          label="Default model"
          control={
            <span className="text-sm break-all text-muted">
              {m.model || m.fallback_model}
            </span>
          }
        >
          {!m.model && <Note>The one this build ships with.</Note>}
        </Field>

        <Row>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setEditing(true)}>Change</Button>
            {m.model !== "" && (
              <Button
                variant="link"
                onClick={async () => {
                  await putAdminDefaultModel("");
                  await client.invalidateQueries({ queryKey: qk.defaultModel });
                }}
              >
                Use the built-in one
              </Button>
            )}
          </div>
        </Row>
      </Section>

      <ModelDialog
        open={editing}
        current={m.model}
        placeholder={m.fallback_model}
        limit={m.model_limit}
        onClose={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          void client.invalidateQueries({ queryKey: qk.defaultModel });
        }}
      />
    </>
  );
}

function ModelDialog({
  open,
  current,
  placeholder,
  limit,
  onClose,
  onSaved,
}: {
  open: boolean;
  current: string;
  placeholder: string;
  limit: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [model, setModel] = useState("");

  // Seeded when the dialog opens, so typing is not overwritten by a refetch underneath.
  useEffect(() => {
    if (open) setModel(current);
  }, [open, current]);

  const save = useMutation({
    mutationFn: putAdminDefaultModel,
    onSuccess: onSaved,
  });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Default model"
      footer={
        <>
          <Button variant="link" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={save.isPending}
            onClick={() => save.mutate(model.trim())}
          >
            {save.isPending ? "saving…" : "Save"}
          </Button>
        </>
      }
    >
      <TextField
        label="Model"
        value={model}
        maxLength={limit}
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        onChange={(e) => setModel(e.target.value)}
      />
      <Note>
        An OpenRouter slug. Leave it empty to follow the one this build ships
        with.
      </Note>
      {save.error && (
        <p className="text-sm text-accent">{save.error.message}</p>
      )}
    </Dialog>
  );
}
