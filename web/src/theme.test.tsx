// The theme toggle: the DOM attribute and the stored choice move together,
// and "automatique" stores NOTHING — the OS keeps deciding through
// `color-scheme`, which is why the absence of the attribute matters as much
// as its two values.

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { applyStoredTheme, ThemeToggle } from "./common.tsx";

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  localStorage.clear();
  delete document.documentElement.dataset.theme;
});

const button = (): HTMLButtonElement => {
  const b = host.querySelector("button.theme");
  if (!(b instanceof HTMLButtonElement)) throw new Error("no theme button");
  return b;
};

describe("the theme toggle", () => {
  it("cycles automatique → sombre → clair, attribute and storage together", () => {
    act(() => {
      root.render(<ThemeToggle />);
    });
    // automatique: nothing pinned, nothing stored
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("paraphe:theme")).toBeNull();

    act(() => {
      button().click();
    });
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem("paraphe:theme")).toBe("dark");

    act(() => {
      button().click();
    });
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(localStorage.getItem("paraphe:theme")).toBe("light");

    // back to automatique: the attribute AND the entry disappear — leaving
    // either behind would pin a scheme the volunteer asked to unpin
    act(() => {
      button().click();
    });
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("paraphe:theme")).toBeNull();
  });

  it("announces the current state and where a press goes", () => {
    act(() => {
      root.render(<ThemeToggle />);
    });
    expect(button().getAttribute("aria-label")).toBe(
      "Thème : automatique — passer en sombre",
    );
    act(() => {
      button().click();
    });
    expect(button().getAttribute("aria-label")).toBe(
      "Thème : sombre — passer en clair",
    );
  });

  it("applyStoredTheme repins the persisted choice before React mounts", () => {
    localStorage.setItem("paraphe:theme", "dark");
    applyStoredTheme();
    expect(document.documentElement.dataset.theme).toBe("dark");
    // and an unknown stored value counts as automatique, not as a scheme
    localStorage.setItem("paraphe:theme", "bizarre");
    applyStoredTheme();
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });
});
