import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteAdminRelay,
  getAdminRelay,
  postAdminRelayTest,
  putAdminRelay,
  type Relay as RelayType,
  type RelayEdit,
} from "@app/api/actions/admin";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Row } from "@app/components/Row";
import { Section } from "@app/components/Section";
import { Select } from "@app/components/Select";
import { TextField } from "@app/components/TextField";
import { Warning } from "@app/components/Warning";

const empty: RelayEdit = {
  host: "",
  port: 587,
  tls: "starttls",
  username: "",
  password: "",
  from_address: "",
  sender_name: "btw",
};

/**
 * The mail relay, which is instance-wide and therefore an administrator's.
 *
 * btw delivers no mail itself. It hands a message to a relay somebody already has, and
 * everything here is about where that is and how to reach it.
 */
export function Relay() {
  const client = useQueryClient();
  const relay = useQuery({ queryKey: qk.relay, queryFn: getAdminRelay });
  const [editing, setEditing] = useState(false);
  const [testing, setTesting] = useState(false);

  if (!relay.isSuccess) return null;
  const r = relay.data;

  return (
    <>
      <Section
        title="Mail relay"
        footer={
          <Note>
            btw does not deliver mail. It hands a message to a relay you already
            have — your provider, or a sending service — and that relay does the
            rest.
          </Note>
        }
      >
        {!r.configured && (
          <Row>
            <Warning>
              No relay is configured, so nobody can add an address to recover
              their account through.
            </Warning>
          </Row>
        )}

        {r.configured && (
          <>
            <Field
              label="Host"
              control={<span className="text-sm text-muted">{r.host}</span>}
            />
            <Field
              label="Port"
              control={
                <span className="text-sm text-muted">
                  {r.port} ·{" "}
                  {r.tls === "implicit" ? "implicit TLS" : "STARTTLS"}
                </span>
              }
            />
            <Field
              label="Signs in as"
              control={
                <span className="text-sm text-muted">
                  {r.username || "no credentials"}
                </span>
              }
            />
            <Field
              label="Sends from"
              control={
                <span className="text-sm text-muted">{r.from_address}</span>
              }
            />
          </>
        )}

        <Row>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setEditing(true)}>
              {r.configured ? "Change" : "Set up a relay"}
            </Button>
            {r.configured && (
              <Button variant="quiet" onClick={() => setTesting(true)}>
                Send a test
              </Button>
            )}
            {r.configured && (
              <Button
                variant="link"
                onClick={async () => {
                  await deleteAdminRelay();
                  await client.invalidateQueries({ queryKey: qk.relay });
                }}
              >
                Forget it
              </Button>
            )}
          </div>
        </Row>
      </Section>

      <RelayDialog
        open={editing}
        current={r}
        onClose={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          void client.invalidateQueries({ queryKey: qk.relay });
        }}
      />
      <TestDialog open={testing} onClose={() => setTesting(false)} />
    </>
  );
}

function RelayDialog({
  open,
  current,
  onClose,
  onSaved,
}: {
  open: boolean;
  current: RelayType;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState<RelayEdit>(empty);

  // Seeded when the dialog opens rather than on every render, so typing is not overwritten
  // by the query refetching underneath.
  useEffect(() => {
    if (!open) return;
    setForm(
      current.configured
        ? {
            host: current.host,
            port: current.port,
            tls: current.tls,
            username: current.username,
            password: "",
            from_address: current.from_address,
            sender_name: current.sender_name,
          }
        : empty,
    );
  }, [open, current]);

  const save = useMutation({ mutationFn: putAdminRelay, onSuccess: onSaved });
  const set = <K extends keyof RelayEdit>(k: K, v: RelayEdit[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Mail relay"
      footer={
        <>
          <Button variant="link" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={save.isPending} onClick={() => save.mutate(form)}>
            {save.isPending ? "saving…" : "Save"}
          </Button>
        </>
      }
    >
      <TextField
        label="Host"
        placeholder="smtp.example.com"
        autoCapitalize="none"
        value={form.host}
        onChange={(e) => set("host", e.target.value)}
      />

      <div className="flex gap-3">
        <div className="w-28">
          <TextField
            label="Port"
            inputMode="numeric"
            value={String(form.port)}
            onChange={(e) =>
              set("port", Number(e.target.value.replace(/\D/g, "")) || 0)
            }
          />
        </div>
        <div className="flex flex-1 flex-col gap-1.5">
          <span className="text-sm font-medium text-fg">Encryption</span>
          {/* Two options and no third. A password crossing the network in the clear is not
              a choice somebody should be able to make by accident. */}
          <Select
            value={form.tls}
            onChange={(e) => set("tls", e.target.value as RelayEdit["tls"])}
          >
            <option value="starttls">STARTTLS (587)</option>
            <option value="implicit">Implicit TLS (465)</option>
          </Select>
        </div>
      </div>

      <TextField
        label="Username"
        autoCapitalize="none"
        autoComplete="off"
        value={form.username}
        onChange={(e) => set("username", e.target.value)}
      />
      <TextField
        label="Password"
        type="password"
        autoComplete="new-password"
        hint={
          current.password_set
            ? "A password is stored. Leave this empty to keep it."
            : undefined
        }
        value={form.password}
        onChange={(e) => set("password", e.target.value)}
      />
      <TextField
        label="Sends from"
        placeholder="btw@example.com"
        autoCapitalize="none"
        hint="The address recipients will see, and the one the relay has to accept."
        value={form.from_address}
        onChange={(e) => set("from_address", e.target.value)}
      />
      <TextField
        label="Sender name"
        value={form.sender_name}
        onChange={(e) => set("sender_name", e.target.value)}
      />

      {save.error && (
        <p className="text-sm text-accent">{save.error.message}</p>
      )}
    </Dialog>
  );
}

/**
 * A test send, which is the whole reason the relay lives in the database rather than the
 * environment: an operator gets it wrong two or three times, and each correction should be
 * a form field and a press rather than a redeploy.
 */
function TestDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [to, setTo] = useState("");
  const test = useMutation({ mutationFn: postAdminRelayTest });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Send a test"
      footer={
        <>
          <Button variant="link" onClick={onClose}>
            Close
          </Button>
          <Button
            disabled={test.isPending || !to}
            onClick={() => test.mutate(to)}
          >
            {test.isPending ? "sending…" : "Send"}
          </Button>
        </>
      }
    >
      <TextField
        label="To"
        placeholder="you@example.com"
        autoCapitalize="none"
        value={to}
        onChange={(e) => setTo(e.target.value)}
      />
      {test.isSuccess && (
        <Note>Sent. If it does not arrive, check the relay's own logs.</Note>
      )}
      {/* The relay's own words, not "sending failed": the host being wrong, the credentials
          being rejected and the certificate not verifying are three different afternoons. */}
      {test.error && (
        <p className="text-sm break-words text-accent">{test.error.message}</p>
      )}
    </Dialog>
  );
}
