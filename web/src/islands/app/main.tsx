import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import "@app/main.css";
import { App } from "@app/islands/app/App";
import { answerClientIdRequests, refresh } from "@app/push";

const client = new QueryClient({
  defaultOptions: {
    queries: {
      // Every read is a server read. There is no client state worth a store, and a stale
      // list of reminders is the one thing this screen must not show after an edit.
      staleTime: 5_000,
      retry: 1,
    },
  },
});

// Re-register whatever subscription this browser holds, on every load. Browsers rotate an
// endpoint without asking, and this is what stops that ending in somebody quietly never
// being nudged again.
void refresh();

// The worker asks who this browser is when it re-subscribes on its own. Nothing else
// listens, and without an answer that re-subscription lands as a second device.
answerClientIdRequests();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
