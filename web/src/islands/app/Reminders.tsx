import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteRemindersById,
  getReminders,
  postReminders,
  postRemindersByIdDone,
  postRemindersByIdRevive,
  patchRemindersById,
  type Reminder,
} from "@app/api/actions/reminders";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { TextArea } from "@app/components/TextArea";
import { TextField } from "@app/components/TextField";
import { IconButton } from "@app/components/IconButton";
import { CheckIcon } from "@app/components/icons/CheckIcon";
import { CrossIcon } from "@app/components/icons/CrossIcon";
import { qk } from "@app/api/keys";

export function Reminders() {
  const [showDone, setShowDone] = useState(false);
  const client = useQueryClient();
  const invalidate = () => {
    void client.invalidateQueries({ queryKey: ["reminders"] });
  };

  const live = useQuery({
    queryKey: qk.reminders(false),
    queryFn: () => getReminders(false),
  });
  const done = useQuery({
    queryKey: qk.reminders(true),
    queryFn: () => getReminders(true),
    enabled: showDone,
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
        onClick={() => setShowDone(!showDone)}
        className="mt-8 text-sm text-faint underline-offset-4 hover:text-fg hover:underline"
      >
        {/* No count. Not here, not on a tag, not in the title, not on the icon. */}
        {showDone ? "hide finished" : "finished"}
      </button>

      {showDone && (
        <ul className="mt-3 divide-y divide-line">
          {done.data?.reminders.map((r) => (
            <FinishedRow key={r.id} reminder={r} onDone={invalidate} />
          ))}
          {done.isSuccess && done.data.reminders.length === 0 && (
            <li className="py-4 text-sm text-faint">Nothing finished yet.</li>
          )}
        </ul>
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
  const end = useMutation({
    mutationFn: postRemindersByIdDone,
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
            own button rather than a click on the row, so it does not swallow Done and Drop
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
        {/* Done and Drop end a reminder identically. The two marks exist because they are
            two different acts — "I did it" and "I do not want this" — and the label on each
            is what says which, since a tick and a cross alone would not. */}
        <IconButton label="Done" onClick={() => end.mutate(reminder.id)}>
          <CheckIcon />
        </IconButton>
        <IconButton label="Drop" onClick={() => end.mutate(reminder.id)}>
          <CrossIcon />
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
 * Ending it is not in here. Done and Drop sit on the row and on the notification, and
 * folding them into a save dialog would make "fix this wording" and "I am finished with
 * this" the same gesture behind the same button.
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

function FinishedRow({
  reminder,
  onDone,
}: {
  reminder: Reminder;
  onDone: () => void;
}) {
  const revive = useMutation({
    mutationFn: postRemindersByIdRevive,
    onSuccess: onDone,
  });
  const remove = useMutation({
    mutationFn: deleteRemindersById,
    onSuccess: onDone,
  });

  return (
    <li className="flex items-baseline gap-3 py-3 text-faint">
      <span className="min-w-0 flex-1 break-words line-through">
        {reminder.text}
      </span>
      <button
        onClick={() => revive.mutate(reminder.id)}
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
