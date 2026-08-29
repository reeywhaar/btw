import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  deleteRemindersById,
  getReminders,
  postReminders,
  postRemindersByIdDone,
  postRemindersByIdRevive,
  type Reminder,
} from "@app/api/actions/reminders";
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
  const end = useMutation({
    mutationFn: postRemindersByIdDone,
    onSuccess: onDone,
  });

  return (
    // items-baseline, not items-start or items-center. The text is 16px and the buttons are
    // 14px inside padding and a border, so aligning the boxes leaves the first line sitting
    // higher than the labels beside it. Baseline aligns what the eye actually reads.
    //
    // And not items-center, because a reminder wraps: centring two lines against the buttons
    // pushes the first line above them and the second below. Baseline uses the *first*
    // line's baseline, so a reminder of any height starts level with Done and grows
    // downward.
    <li className="flex items-baseline gap-3 py-3">
      <span className="min-w-0 flex-1 break-words">{reminder.text}</span>
      {/* Done and Drop end a reminder identically. The two words exist because they are
          two different acts — "I did it" and "I do not want this" — and a product with
          only Done makes ending something you never did feel like a small lie. */}
      <button
        onClick={() => end.mutate(reminder.id)}
        className="shrink-0 rounded-md border border-line px-3 py-1 text-sm text-muted hover:border-line-strong hover:text-fg"
      >
        Done
      </button>
      <button
        onClick={() => end.mutate(reminder.id)}
        className="shrink-0 rounded-md border border-line px-3 py-1 text-sm text-faint hover:border-line-strong hover:text-fg"
      >
        Drop
      </button>
    </li>
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
