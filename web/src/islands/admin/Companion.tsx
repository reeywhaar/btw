import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  getAdminDefaultModel,
  putAdminDefaultModel,
  type ServiceDefault,
} from "@app/api/actions/admin";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Section } from "@app/components/Section";
import { TextField } from "@app/components/TextField";

/**
 * The model an account on each service gets without naming one.
 *
 * Instance-wide and an administrator's, unlike the key each account brings. It exists because
 * routers retire slugs: without it, the day a compiled-in model goes, every account that never
 * chose one breaks at once and the only fix is a new build.
 *
 * One per service, because a slug belongs to one — an OpenRouter name means nothing to the
 * Hugging Face router.
 */
export function Companion() {
  const client = useQueryClient();
  const defaults = useQuery({
    queryKey: qk.defaultModel,
    queryFn: getAdminDefaultModel,
  });
  const [editing, setEditing] = useState<ServiceDefault | null>(null);

  if (!defaults.isSuccess) return null;
  const invalidate = () =>
    client.invalidateQueries({ queryKey: qk.defaultModel });

  return (
    <>
      <Section
        title="Companion"
        footer={
          <Note>
            Anyone who has typed a model of their own keeps it. Nothing is
            checked here — the instance has no key to check it with, and the
            first companion to use one reports what the gateway said.
          </Note>
        }
      >
        {defaults.data.providers.map((s) => (
          <Field
            key={s.provider}
            label={s.label}
            control={
              <Button variant="quiet" onClick={() => setEditing(s)}>
                Change
              </Button>
            }
          >
            <Note>
              {s.model || `${s.fallback_model} · the one this build ships with`}
            </Note>
          </Field>
        ))}
      </Section>

      <ModelDialog
        service={editing}
        limit={defaults.data.model_limit}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          void invalidate();
        }}
      />
    </>
  );
}

function ModelDialog({
  service,
  limit,
  onClose,
  onSaved,
}: {
  service: ServiceDefault | null;
  limit: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [model, setModel] = useState("");

  // Seeded when the dialog opens, so typing is not overwritten by a refetch underneath.
  useEffect(() => {
    if (service) setModel(service.model);
  }, [service]);

  const save = useMutation({
    mutationFn: (next: string) =>
      putAdminDefaultModel(service?.provider ?? "", next),
    onSuccess: onSaved,
  });

  return (
    <Dialog
      open={service !== null}
      onClose={onClose}
      title={service ? `${service.label} default` : "Default model"}
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
        placeholder={service?.fallback_model}
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        onChange={(e) => setModel(e.target.value)}
      />
      <Note>
        A slug this service knows. Leave it empty to follow the one this build
        ships with.
      </Note>
      {save.error && (
        <p className="text-sm text-accent">{save.error.message}</p>
      )}
    </Dialog>
  );
}
