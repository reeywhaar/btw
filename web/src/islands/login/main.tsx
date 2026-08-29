import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "@app/main.css";
import { Login } from "@app/islands/login/Login";

// No QueryClient here. This island makes at most two requests and then navigates away;
// a cache with nothing to keep is a dependency with nothing to do.
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Login />
  </StrictMode>,
);
