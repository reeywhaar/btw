import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteAdminProxy,
  getAdminProxy,
  patchAdminProxy,
  postAdminProxyTest,
  putAdminProxy,
  type Proxy as ProxyType,
  type ProxyEdit,
} from "@app/api/actions/admin";
import { qk } from "@app/api/keys";
import { Button } from "@app/components/Button";
import { Check } from "@app/components/Check";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { Note } from "@app/components/Note";
import { Row } from "@app/components/Row";
import { Section } from "@app/components/Section";
import { Select } from "@app/components/Select";
import { TextField } from "@app/components/TextField";

const empty: ProxyEdit = {
  kind: "proxio",
  url: "",
  username: "",
  token: "",
};

/** The address a kind is written as, so a placeholder and a refusal agree about the shape. */
const example = {
  proxio: "https://proxio.example.com",
  socks5: "socks5://socks.example.com:1080",
} as const;

/**
 * How this instance reaches the internet, which is instance-wide and therefore an
 * administrator's — unlike the key each account brings for its own companion.
 *
 * btw reaches exactly one thing out there, so there is no list and no order to try things in.
 * One proxy, switched on or off.
 */
export function Proxy() {
  const client = useQueryClient();
  const proxy = useQuery({ queryKey: qk.proxy, queryFn: getAdminProxy });
  const [editing, setEditing] = useState(false);
  const [testing, setTesting] = useState(false);

  const invalidate = () => client.invalidateQueries({ queryKey: qk.proxy });
  const toggle = useMutation({
    mutationFn: patchAdminProxy,
    onSuccess: () => void invalidate(),
  });

  if (!proxy.isSuccess) return null;
  const p = proxy.data;

  return (
    <>
      <Section
        title="Proxy"
        footer={
          <Note>
            Only what a companion sends goes through it. Nothing else here
            reaches the internet at all.
          </Note>
        }
      >
        {p.configured && (
          <>
            <Field
              label="Kind"
              control={
                <span className="text-sm text-muted">
                  {p.kind === "socks5" ? "SOCKS5" : "proxio"}
                </span>
              }
            />
            <Field
              label="Address"
              control={
                <span className="text-sm break-all text-muted">{p.url}</span>
              }
            />
            {p.kind === "socks5" && (
              <Field
                label="Signs in as"
                control={
                  <span className="text-sm text-muted">{p.username}</span>
                }
              />
            )}
            <Field
              label="In use"
              control={
                <Check checked={p.enabled} onChange={(v) => toggle.mutate(v)} />
              }
            >
              {!p.enabled && (
                // Said out loud, because a proxy that is configured and switched off looks
                // exactly like one that is working until somebody reads the checkbox.
                <Note>
                  Requests are going out directly. The address and the
                  credential are kept.
                </Note>
              )}
            </Field>
          </>
        )}

        <Row>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setEditing(true)}>
              {p.configured ? "Change" : "Set up a proxy"}
            </Button>
            {p.configured && (
              <Button variant="quiet" onClick={() => setTesting(true)}>
                Try it
              </Button>
            )}
            {p.configured && (
              <Button
                variant="link"
                onClick={async () => {
                  await deleteAdminProxy();
                  await invalidate();
                }}
              >
                Forget it
              </Button>
            )}
          </div>
        </Row>
      </Section>

      <ProxyDialog
        open={editing}
        current={p}
        onClose={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          void invalidate();
        }}
      />
      <TestDialog open={testing} onClose={() => setTesting(false)} />
    </>
  );
}

function ProxyDialog({
  open,
  current,
  onClose,
  onSaved,
}: {
  open: boolean;
  current: ProxyType;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState<ProxyEdit>(empty);

  // Seeded when the dialog opens rather than on every render, so typing is not overwritten by
  // the query refetching underneath.
  useEffect(() => {
    if (!open) return;
    setForm(
      current.configured && current.kind !== ""
        ? {
            kind: current.kind,
            url: current.url,
            username: current.username,
            token: "",
          }
        : empty,
    );
  }, [open, current]);

  const save = useMutation({ mutationFn: putAdminProxy, onSuccess: onSaved });
  const set = <K extends keyof ProxyEdit>(k: K, v: ProxyEdit[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  // The endpoint is what decides whether an empty token means "keep the stored one". Saying so
  // beside the field is cheaper than a refusal somebody has to read twice.
  const sameEndpoint =
    current.token_set &&
    current.kind === form.kind &&
    current.url === form.url.trim();

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Proxy"
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
      <div className="flex flex-col gap-1.5">
        <span className="text-sm font-medium text-fg">Kind</span>
        {/* Two, and they are not variations on each other: one rewrites the request's URL and
            the other replaces the connection under it, which is why the fields below change. */}
        <Select
          value={form.kind}
          onChange={(e) => set("kind", e.target.value as ProxyEdit["kind"])}
        >
          <option value="proxio">proxio</option>
          <option value="socks5">SOCKS5</option>
        </Select>
      </div>

      <TextField
        label="Address"
        placeholder={example[form.kind]}
        autoCapitalize="none"
        autoComplete="off"
        hint={
          form.kind === "proxio"
            ? "The host on its own. Pasting the whole /proxy?url=…&token=… example works too — the token is taken out of it."
            : "socks5:// or socks5h://, which behave identically here."
        }
        value={form.url}
        onChange={(e) => set("url", e.target.value)}
      />

      {form.kind === "socks5" && (
        <TextField
          label="Username"
          autoCapitalize="none"
          autoComplete="off"
          value={form.username}
          onChange={(e) => set("username", e.target.value)}
        />
      )}

      <TextField
        label={form.kind === "socks5" ? "Password" : "Token"}
        type="password"
        autoComplete="new-password"
        hint={
          sameEndpoint
            ? "One is stored. Leave this empty to keep it."
            : current.token_set
              ? "The stored one belongs to the address above it, so this needs filling in."
              : undefined
        }
        value={form.token}
        onChange={(e) => set("token", e.target.value)}
      />

      <Note>Saving switches the proxy on.</Note>

      {save.error && (
        <p className="text-sm text-accent">{save.error.message}</p>
      )}
    </Dialog>
  );
}

/**
 * One request through the saved proxy, to the one host it exists to reach.
 *
 * Not to an address somebody types. A proxy that reaches everything except the gateway is a
 * proxy somebody would otherwise have called working — and it is the whole failure this is
 * worth guarding against, since a container on the same network as btw goes out from the same
 * address and tests fine against anything unrestricted.
 */
function TestDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const test = useMutation({ mutationFn: postAdminProxyTest });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Try it"
      footer={
        <>
          <Button variant="link" onClick={onClose}>
            Close
          </Button>
          <Button disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "trying…" : "Try"}
          </Button>
        </>
      }
    >
      <Note>
        Fetches openrouter.ai through the proxy — the one host a companion talks
        to. It works whether or not the proxy is switched on.
      </Note>
      {test.isSuccess && (
        <p className="text-sm text-muted">
          It got through in {test.data.took_ms} ms.
        </p>
      )}
      {/* Whatever failed, in its own words — with any address scrubbed out of it server-side,
          because a transport error names what it dialled and that ends in the token. */}
      {test.error && (
        <p className="text-sm break-words text-accent">{test.error.message}</p>
      )}
    </Dialog>
  );
}
