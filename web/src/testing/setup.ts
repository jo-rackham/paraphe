// IndexedDB does not exist in jsdom: this in-memory implementation makes
// local tracking, export and import testable — the only thing browser mode
// owns, hence the only thing it can lose.
import "fake-indexeddb/auto";

// The address bar is real state, and jsdom keeps ONE per test file: a test
// that navigates leaves the next one starting on its screen instead of the
// application's home. Reset before each, so every test opens where a visitor
// would — and so a test asserting a route asserts its own navigation.
import { beforeEach } from "vitest";

beforeEach(() => {
  window.history.replaceState(null, "", "/");
});
