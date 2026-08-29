import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";

import {
  getAuthMe,
  postAuthLogout,
  postAuthPassword,
} from "@app/api/actions/auth";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Section } from "@app/components/Section";
import { TextField } from "@app/components/TextField";
import { Recovery } from "@app/islands/app/Recovery";

/**
 * Everything about the account rather than about the reminders.
 *
 * Its own page because it is a different subject from the rhythm and the devices, and
 * because the things on it — a password, an address, signing out — are the ones somebody
 * arrives looking for rather than stumbles into while adjusting something else.
 */
export function Account() {
  const me = useQuery({ queryKey: qk.me, queryFn: getAuthMe });
  const [changing, setChanging] = useState(false);

  return (
    <main className="space-y-8 px-4">
      <Section title="Account">
        <Field
          label="Signed in as"
          control={
            <span className="text-sm text-muted">
              {me.data?.username ?? "…"}
            </span>
          }
        />
        <Field
          label="Password"
          control={
            <Button variant="link" onClick={() => setChanging(true)}>
              change
            </Button>
          }
        />
      </Section>

      <Recovery />

      {/* No heading. "Signing out" as a section title names the act rather than a group of
          settings, and the button says it already. */}
      <section>
        <Button
          variant="quiet"
          onClick={async () => {
            await postAuthLogout();
            window.location.replace("/login");
          }}
        >
          Sign out
        </Button>
      </section>

      <PasswordDialog open={changing} onClose={() => setChanging(false)} />
    </main>
  );
}

function PasswordDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [done, setDone] = useState(false);

  const change = useMutation({
    mutationFn: () => postAuthPassword(current, next),
    onSuccess: () => {
      setCurrent("");
      setNext("");
      setDone(true);
    },
  });

  return (
    <Dialog
      open={open}
      onClose={() => {
        setDone(false);
        change.reset();
        onClose();
      }}
      title="Change your password"
      footer={
        done ? (
          <Button
            onClick={() => {
              setDone(false);
              change.reset();
              onClose();
            }}
          >
            Close
          </Button>
        ) : (
          <>
            <Button variant="link" onClick={onClose}>
              Cancel
            </Button>
            <Button
              disabled={change.isPending || !current || !next}
              onClick={() => change.mutate()}
            >
              {change.isPending ? "changing…" : "Change it"}
            </Button>
          </>
        )
      }
    >
      {done ? (
        <Note>
          Changed. Every other device you were signed in on has been signed out;
          this one stayed.
        </Note>
      ) : (
        <>
          <TextField
            label="Current password"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
          <TextField
            label="New password"
            type="password"
            autoComplete="new-password"
            hint="At least eight characters."
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
          <Note>
            Every other device you are signed in on will be signed out. This one
            stays.
          </Note>
          {change.error && (
            <p className="text-sm text-accent">{change.error.message}</p>
          )}
        </>
      )}
    </Dialog>
  );
}
