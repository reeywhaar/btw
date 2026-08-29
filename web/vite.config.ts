import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { resolve } from "node:path";

// Two entries, one per audience. login.html is the only document an unauthenticated
// visitor loads: a few kilobytes instead of the whole application, and an invitation link
// handed to somebody who has never heard of this instance opens a page about accepting an
// invitation rather than the shell of an app they cannot use.
export default defineConfig({
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: { "@app": resolve(import.meta.dirname, "src") },
  },
  build: {
    rollupOptions: {
      input: {
        index: resolve(import.meta.dirname, "index.html"),
        login: resolve(import.meta.dirname, "login.html"),
      },
    },
  },
  server: {
    // `npm run dev` proxies the API to a locally running binary, so the frontend can be
    // worked on without rebuilding Go.
    proxy: {
      "/api": "http://localhost:3004",
      "/healthz": "http://localhost:3004",
    },
  },
});
