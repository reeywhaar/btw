import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import "@app/main.css";
import { Admin } from "@app/islands/admin/Admin";

const client = new QueryClient({
  defaultOptions: { queries: { staleTime: 5_000, retry: 1 } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <Admin />
    </QueryClientProvider>
  </StrictMode>,
);
