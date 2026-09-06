import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteRemindersById,
  getReminders,
  postReminders,
  postRemindersByIdBin,
  postRemindersByIdRestore,
  patchRemindersById,
  type Reminder,
} from "@app/api/actions/reminders";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
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

  const live = useQuery({
    queryKey: qk.reminders(false),
    queryFn: () => getReminders(false),
  });
  const binned = useQuery({
    queryKey: qk.reminders(true),
    queryFn: () => getReminders(true),
    enabled: showBin,
  });

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
        onClick={() => setShowBin(!showBin)}
        className="mt-8 text-sm text-faint underline-offset-4 hover:text-fg hover:underline"
      >
        {/* No count. Not here, not on a tag, not in the title, not on the icon. */}
        {showBin ? "hide bin" : "bin"}
      </button>

      {showBin && (
        <>
          <ul className="mt-3 divide-y divide-line">
            {binned.data?.reminders.map((r) => (
              <BinnedRow key={r.id} reminder={r} onDone={invalidate} />
            ))}
            {binned.isSuccess && binned.data.reminders.length === 0 && (
              <li className="py-4 text-sm text-faint">The bin is empty.</li>
            )}
          </ul>
          {binned.isSuccess && binned.data.reminders.length > 0 && (
            // Said once, under the list, rather than as a countdown on each row. A number
            // ticking down beside something somebody has finished with is exactly the kind
            // this product exists not to show.
            <p className="mt-3 text-sm text-faint">
              Anything left here for thirty days is thrown away.
            </p>
          )}
        </>
      )}
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
  const bin = useMutation({
    mutationFn: postRemindersByIdBin,
    onSuccess: onDone,
  });

  return (
    <>
      {/* items-baseline, not items-start or items-center. The text is 16px and the buttons
          are 14px inside padding and a border, so aligning the boxes leaves the first line
          sitting higher than the labels beside it. Baseline aligns what the eye reads.

          And not items-center, because a reminder wraps: centring two lines against the
          buttons pushes the first above them and the second below. Baseline uses the *first*
          line's baseline, so a reminder of any height starts level with Done. */}
      {/* items-start, not items-baseline. Baseline was matching the sentence to a button's
          *label*; an icon button has no text in it, so there is nothing to align to and the
          marks drifted. Aligning the tops and giving the sentence a hair of padding puts its
          first line level with the marks and lets it wrap downward. */}
      <li className="flex items-start gap-1 py-2">
        {/* The sentence is the way in, because it is the thing somebody is looking at. Its
            own button rather than a click on the row, so it does not swallow the bin
            or nest one control inside another. */}
        <button
          onClick={() => setEditing(true)}
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
        {/* One mark. It was a tick and a cross, which ended a reminder identically and
            differed only in the word beside them — a to-do list's *done* and *drop*, where the
            second existed so that finishing something never started did not mean claiming
            otherwise. A bin claims neither, and says where the thing actually goes. */}
        <IconButton label="Bin" onClick={() => bin.mutate(reminder.id)}>
          <BinIcon />
        </IconButton>
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
 * Editing what a reminder says.
 *
 * Binning it is not in here. The bin sits on the row and on the notification, and folding it
 * into a save dialog would make "fix this wording" and "I am finished with this" the same
 * gesture behind the same button.
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
