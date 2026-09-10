import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteBin,
  deleteRemindersById,
  getBin,
  getReminders,
  postReminders,
  postRemindersByIdBin,
  postRemindersByIdRestore,
  patchRemindersById,
  type Reminder,
} from "@app/api/actions/reminders";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Note } from "@app/components/Note";
import { TextArea } from "@app/components/TextArea";
import { TextField } from "@app/components/TextField";
import { IconButton } from "@app/components/IconButton";
import { BinIcon } from "@app/components/icons/BinIcon";
import { qk } from "@app/api/keys";

export function Reminders() {
  const [showBin, setShowBin] = useState(false);
  const client = useQueryClient();
  const invalidate = () => {
    void client.invalidateQueries({ queryKey: ["reminders"] });
  };

  const live = useQuery({ queryKey: qk.reminders, queryFn: getReminders });

  return (
    <main className="px-5">
      <Compose onDone={invalidate} />

      {live.isSuccess && live.data.reminders.length === 0 && (
        <p className="py-10 text-center text-sm text-faint">
          Nothing written down. Whatever you keep meaning to do goes here — you
          do not have to say when.
        </p>
      )}

      <ul className="divide-y divide-line">
        {live.data?.reminders.map((r) => (
          <Row key={r.id} reminder={r} onDone={invalidate} />
        ))}
      </ul>

      <button
        onClick={() => setShowBin(true)}
        className="mt-8 text-sm text-faint underline-offset-4 hover:text-fg hover:underline"
      >
        {/* No count. Not here, not on a tag, not in the title, not on the icon. */}
        bin
      </button>

      <BinDialog
        open={showBin}
        onClose={() => setShowBin(false)}
        onChanged={invalidate}
      />
    </main>
  );
}

function Compose({ onDone }: { onDone: () => void }) {
  const [text, setText] = useState("");
  const create = useMutation({
    mutationFn: postReminders,
    onSuccess: () => {
      setText("");
      onDone();
    },
  });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const value = text.trim();
        // One field, and pressing return is the entire path to a reminder existing. No
        // dialog, no second step, nothing else required.
        if (value) create.mutate(value);
      }}
      className="pb-4"
    >
      <input
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="btw, …"
        autoCapitalize="sentences"
        className="w-full rounded-lg border border-line bg-surface px-4 py-3 text-fg placeholder:text-faint focus:border-accent/60 focus:outline-none"
      />
      {create.error && (
        <p className="pt-2 text-sm text-accent">{create.error.message}</p>
      )}
    </form>
  );
}

function Row({ reminder, onDone }: { reminder: Reminder; onDone: () => void }) {
  const [editing, setEditing] = useState(false);
  const client = useQueryClient();

  // Binning greys the row where it is rather than making it vanish under the finger that
  // pressed it. Marked in the cache, since the reminder already carries `binned_at` and the
  // server has just set it, and it lasts until the next refetch — undo is for a press that was
  // a mistake, and a mistake is noticed immediately or not at all.
  const mark = (at: number | null) =>
    client.setQueryData<{ reminders: Reminder[] }>(qk.reminders, (old) =>
      old
        ? {
            reminders: old.reminders.map((r) =>
              r.id === reminder.id ? { ...r, binned_at: at } : r,
            ),
          }
        : old,
    );
  const settle = () => {
    void client.invalidateQueries({ queryKey: qk.bin });
  };

  const bin = useMutation({
    mutationFn: postRemindersByIdBin,
    onSuccess: () => {
      mark(Math.floor(Date.now() / 1000));
      settle();
    },
  });
  const undo = useMutation({
    mutationFn: postRemindersByIdRestore,
    onSuccess: () => {
      mark(null);
      settle();
    },
  });

  const isBinned = reminder.binned_at !== null;

  return (
    <>
      {/* items-start, not items-baseline. Baseline was matching the sentence to a button's
          *label*; an icon button has no text in it, so there is nothing to align to and the
          marks drifted. Aligning the tops and giving the sentence a hair of padding puts its
          first line level with the marks and lets it wrap downward. */}
      <li
        className={`flex items-start gap-1 py-2 ${isBinned ? "text-faint" : ""}`}
      >
        {/* The sentence is the way in, because it is the thing somebody is looking at. Its
            own button rather than a click on the row, so it does not swallow the bin
            or nest one control inside another. */}
        <button
          onClick={() => setEditing(true)}
          disabled={isBinned}
          className="min-w-0 flex-1 py-1.5 text-left"
          aria-label={`Edit ${reminder.text}`}
        >
          <span className="block break-words">{reminder.text}</span>
          {reminder.note && (
            // One line of it, so a description is worth adding without turning the list
            // into the thing this product is trying not to be.
            <span className="mt-0.5 block truncate text-sm text-faint">
              {reminder.note}
            </span>
          )}
        </button>
        {isBinned ? (
          // A word rather than a mark. Undo is the opposite of the thing just pressed, and an
          // arrow beside a bin would be one icon asking to be told apart from another.
          <button
            onClick={() => undo.mutate(reminder.id)}
            className="shrink-0 py-1.5 text-sm underline-offset-4 hover:text-fg hover:underline"
          >
            undo
          </button>
        ) : (
          /* One mark. It was a tick and a cross, which ended a reminder identically and
             differed only in the word beside them — a to-do list's *done* and *drop*, where
             the second existed so that finishing something never started did not mean
             claiming otherwise. A bin claims neither, and says where the thing goes. */
          <IconButton label="Bin" onClick={() => bin.mutate(reminder.id)}>
            {/* Fainter than the sentence beside it. It is on every reminder and wanted on
                almost none of them, so it should be findable rather than present. */}
            <BinIcon className="opacity-40" />
          </IconButton>
        )}
      </li>

      <EditDialog
        open={editing}
        reminder={reminder}
        onClose={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onDone();
        }}
      />
    </>
  );
}

/**
 * Editing what a reminder says. Binning is not in here: it sits on the row and on the
 * notification, and folding it in would put "fix this wording" and "I am done with this" behind
 * one button.
 */
function EditDialog({
  open,
  reminder,
  onClose,
  onSaved,
}: {
  open: boolean;
  reminder: Reminder;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [text, setText] = useState(reminder.text);
  const [note, setNote] = useState(reminder.note);

  // Seeded when it opens, not on every render, so typing is not overwritten by the list
  // refetching underneath.
  useEffect(() => {
    if (!open) return;
    setText(reminder.text);
    setNote(reminder.note);
  }, [open, reminder.text, reminder.note]);

  const save = useMutation({
    mutationFn: () => patchRemindersById(reminder.id, { text, note }),
    onSuccess: onSaved,
  });
  const remove = useMutation({
    mutationFn: () => deleteRemindersById(reminder.id),
    onSuccess: onSaved,
  });

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Edit reminder"
      footer={
        <>
          {/* Deleting is not ending. Ending is Done or Drop and keeps the record; this is
              for the one typed by mistake, so it sits apart from the save. */}
          <Button
            variant="link"
            disabled={remove.isPending}
            onClick={() => remove.mutate()}
          >
            Delete
          </Button>
          <span className="flex-1" />
          <Button variant="link" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={save.isPending || !text.trim()}
            onClick={() => save.mutate()}
          >
            {save.isPending ? "saving…" : "Save"}
          </Button>
        </>
      }
    >
      <TextField
        label="Reminder"
        value={text}
        onChange={(e) => setText(e.target.value)}
        hint="What arrives on your phone. Keep it to a sentence."
      />
      <TextArea
        label="Description"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Anything the sentence could not hold."
        hint="Only ever seen here — a notification carries the sentence alone."
      />
      {save.error && (
        <p className="text-sm text-accent">{save.error.message}</p>
      )}
      {remove.error && (
        <p className="text-sm text-accent">{remove.error.message}</p>
      )}
    </Dialog>
  );
}

/**
 * The bin, as a place somebody goes: unfolding it in place would put a list of things they have
 * finished with directly beneath the list of things they have not. A dialog is a room you leave.
 */
function BinDialog({
  open,
  onClose,
  onChanged,
}: {
  open: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const client = useQueryClient();
  const [confirming, setConfirming] = useState(false);

  const bin = useQuery({
    queryKey: qk.bin,
    queryFn: getBin,
    enabled: open,
  });

  const changed = () => {
    void client.invalidateQueries({ queryKey: qk.bin });
    onChanged();
  };
  const empty = useMutation({
    mutationFn: deleteBin,
    onSuccess: () => {
      setConfirming(false);
      changed();
    },
  });

  // Nothing left over from a previous visit: a dialog reopened on "really?" would be one press
  // from throwing away something somebody came back to rescue.
  useEffect(() => {
    if (!open) setConfirming(false);
  }, [open]);

  const items = bin.data?.reminders ?? [];

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Bin"
      footer={
        <>
          <Button variant="link" onClick={onClose}>
            Close
          </Button>
          {items.length > 0 &&
            (confirming ? (
              // Asked, because this is the one press here that cannot be undone. The per-row
              // delete is one thing at a time and this is everything at once.
              <Button disabled={empty.isPending} onClick={() => empty.mutate()}>
                {empty.isPending ? "clearing…" : "Yes, throw it all away"}
              </Button>
            ) : (
              <Button variant="quiet" onClick={() => setConfirming(true)}>
                Clean up
              </Button>
            ))}
        </>
      }
    >
      {items.length === 0 && bin.isSuccess && <Note>The bin is empty.</Note>}

      {items.length > 0 && (
        <ul className="divide-y divide-line">
          {items.map((r) => (
            <BinnedRow key={r.id} reminder={r} onDone={changed} />
          ))}
        </ul>
      )}

      {items.length > 0 && (
        // Said once, under the list, rather than as a countdown on each row. A number ticking
        // down beside something somebody has finished with is exactly the kind this product
        // exists not to show.
        <Note>Anything left here for thirty days is thrown away.</Note>
      )}

      {empty.error && (
        <p className="text-sm text-accent">{empty.error.message}</p>
      )}
    </Dialog>
  );
}

function BinnedRow({
  reminder,
  onDone,
}: {
  reminder: Reminder;
  onDone: () => void;
}) {
  const restore = useMutation({
    mutationFn: postRemindersByIdRestore,
    onSuccess: onDone,
  });
  const remove = useMutation({
    mutationFn: deleteRemindersById,
    onSuccess: onDone,
  });

  return (
    <li className="flex items-baseline gap-3 py-3 text-faint">
      {/* Not struck through. A line through it says "done", which is the claim the bin was
          brought in to stop making — this one may simply not be wanted. */}
      <span className="min-w-0 flex-1 break-words">{reminder.text}</span>
      <button
        onClick={() => restore.mutate(reminder.id)}
        className="shrink-0 text-sm underline-offset-4 hover:text-fg hover:underline"
      >
        put back
      </button>
      <button
        onClick={() => remove.mutate(reminder.id)}
        className="shrink-0 text-sm underline-offset-4 hover:text-accent hover:underline"
      >
        delete
      </button>
    </li>
  );
}
