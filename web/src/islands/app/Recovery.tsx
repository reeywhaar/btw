import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteAuthRecovery,
  getAuthRecovery,
  postAuthRecovery,
  postAuthRecoveryConfirm,
} from "@app/api/actions/auth";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Row } from "@app/components/Row";
import { Section } from "@app/components/Section";
import { TextField } from "@app/components/TextField";
import { Warning } from "@app/components/Warning";

/**
 * An address the account can be recovered through.
 *
 * Only an address somebody has proved they can read. Adding one is two steps — a code goes
 * to the address and has to come back — and until it does the account has no recovery
 * address at all, not a provisional one, so a flow abandoned anywhere leaves exactly what
 * was there before.
 *
 * Storing whatever was typed is worse than storing nothing: a typo points recovery at a
 * stranger's inbox and the owner finds out at the one moment they cannot afford to.
 */
export function Recovery() {
  const client = useQueryClient();
  const recovery = useQuery({
    queryKey: qk.recovery,
    queryFn: getAuthRecovery,
  });
  const [adding, setAdding] = useState(false);

  if (!recovery.isSuccess) return null;
  const r = recovery.data;

  // No relay and no address is a section with nothing in it and nothing to do about it.
  // Configuring a relay is an administrator's job, and explaining that on everybody's
  // settings is an explanation aimed at somebody who is not reading it.
  //
  // An address already on file keeps the section even when the relay goes away, so it can
  // still be seen and forgotten. What it cannot do then is change, which is what the
  // warning below says.
  if (!r.mail_configured && !r.email) return null;
  const refresh = () => client.invalidateQueries({ queryKey: qk.recovery });

  return (
    <>
      <Section
        title="Recovery"
        footer={
          <Note>
            One address, and only one somebody has proved they can read. It is
            not shown to anybody else and nothing else is sent to it.
          </Note>
        }
      >
        {!r.mail_configured && (
          <Row>
            {/* Only reachable with an address already on file: this instance could send
                once and cannot now, which is the difference between "recovery works" and
                "recovery worked". */}
            <Warning>
              This instance can no longer send mail, so this address cannot be
              changed or re-proved until an administrator configures a relay.
            </Warning>
          </Row>
        )}

        {r.email ? (
          <Field
            label={r.email}
            control={
              <Button
                variant="link"
                onClick={async () => {
                  await deleteAuthRecovery();
                  await refresh();
                }}
              >
                forget
              </Button>
            }
          />
        ) : (
          <Row>
            <p className="text-sm text-muted">
              No address on file. Without one there is no way back into this
              account except an administrator.
            </p>
          </Row>
        )}

        {r.mail_configured && (
          <Row>
            <Button variant="quiet" onClick={() => setAdding(true)}>
              {r.email ? "Use a different address" : "Add an address"}
            </Button>
          </Row>
        )}
      </Section>

      <AddDialog
        open={adding}
        pending={r.pending}
        onClose={() => setAdding(false)}
        onDone={() => {
          setAdding(false);
          void refresh();
        }}
      />
    </>
  );
}

/**
 * Two steps in one dialog: send a code, then type it back.
 *
 * It opens on the second step when there is already an attempt in flight, so closing the
 * dialog and coming back does not mean asking for a second code — and two live codes for
 * one account is two chances at the same guess.
 */
function AddDialog({
  open,
  pending,
  onClose,
  onDone,
}: {
  open: boolean;
  pending: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [sentTo, setSentTo] = useState("");

  const waitingFor = sentTo || pending;

  const send = useMutation({
    mutationFn: postAuthRecovery,
    onSuccess: (_, address) => setSentTo(address),
  });
  const confirm = useMutation({
    mutationFn: postAuthRecoveryConfirm,
    onSuccess: () => {
      setEmail("");
      setCode("");
      setSentTo("");
      onDone();
    },
  });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={waitingFor ? "Type the code" : "Add an address"}
      footer={
        waitingFor ? (
          <>
            <Button variant="link" onClick={onClose}>
              Cancel
            </Button>
            <Button
              disabled={confirm.isPending || !code}
              onClick={() => confirm.mutate(code)}
            >
              {confirm.isPending ? "checking…" : "Confirm"}
            </Button>
          </>
        ) : (
          <>
            <Button variant="link" onClick={onClose}>
              Cancel
            </Button>
            <Button
              disabled={send.isPending || !email}
              onClick={() => send.mutate(email)}
            >
              {send.isPending ? "sending…" : "Send a code"}
            </Button>
          </>
        )
      }
    >
      {waitingFor ? (
        <>
          <p className="text-sm text-muted">
            A code went to <span className="text-fg">{waitingFor}</span>. It is
            good for fifteen minutes.
          </p>
          <TextField
            label="Code"
            autoCapitalize="characters"
            autoComplete="one-time-code"
            spellCheck={false}
            placeholder="ABCD1234"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          {confirm.error && (
            <p className="text-sm text-accent">{confirm.error.message}</p>
          )}
          <Button
            variant="link"
            onClick={() => {
              setSentTo("");
              setCode("");
            }}
          >
            Send it somewhere else
          </Button>
        </>
      ) : (
        <>
          <TextField
            label="Address"
            type="email"
            autoCapitalize="none"
            autoComplete="email"
            placeholder="you@example.com"
            hint="A code goes there and has to come back. Nothing is stored until it does."
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          {send.error && (
            <p className="text-sm break-words text-accent">
              {send.error.message}
            </p>
          )}
        </>
      )}
    </Dialog>
  );
}
