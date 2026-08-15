import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import { applyStoredTheme } from "./common.tsx";
import "./style.css";

// before the first render: a persisted theme choice must win over the OS
// from the very first painted frame
applyStoredTheme();

const root = document.getElementById("root");
if (!root) throw new Error("élément #root absent de index.html");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
