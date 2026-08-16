import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import { takeLinkToken } from "./api.ts";
import { applyStoredTheme } from "./common.tsx";
import "./style.css";

// before the first render: a persisted theme choice must win over the OS
// from the very first painted frame
applyStoredTheme();

// And before ANY of it: a sign-in link's token comes out of the address bar
// here, not inside the screen that will use it. Taken there, it survived
// every path that never reaches that screen — the server being down, the
// account-less version, a mode detection that answers something else — and
// sat in the URL, in the history entry and in whatever the visitor pastes
// into a support thread, for as long as the outage lasted.
takeLinkToken();

const root = document.getElementById("root");
if (!root) throw new Error("élément #root absent de index.html");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
