import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Alerte } from "./common.tsx";
import type { Message } from "./types.ts";

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.useRealTimers();
});

const show = (message: Message | null, onClose: () => void) =>
  act(() => {
    root.render(<Alerte message={message} onClose={onClose} />);
  });

describe("Alerte", () => {
  it("dismisses a success on its own after seven seconds", () => {
    const onClose = vi.fn();
    show({ tone: "ok", text: "Enregistré." }, onClose);
    act(() => vi.advanceTimersByTime(6900));
    expect(onClose).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(200));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("counts from the message, not from the last render", () => {
    // parents pass an inline arrow whose identity changes on every render:
    // a timer armed on the CALLBACK would be reset for ever
    const message: Message = { tone: "ok", text: "Enregistré." };
    const first = vi.fn();
    const second = vi.fn();
    show(message, first);
    act(() => vi.advanceTimersByTime(5000));
    show(message, second);
    act(() => vi.advanceTimersByTime(2100));
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it("an error stays until acted on", () => {
    const onClose = vi.fn();
    show({ tone: "erreur", text: "Écriture refusée." }, onClose);
    act(() => vi.advanceTimersByTime(60_000));
    expect(onClose).not.toHaveBeenCalled();
  });
});
