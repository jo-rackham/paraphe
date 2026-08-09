import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import "./style.css";

const root = document.getElementById("root");
if (!root) throw new Error("élément #root absent de index.html");

createRoot(root).render(
  <StrictMode><App /></StrictMode>,
);
