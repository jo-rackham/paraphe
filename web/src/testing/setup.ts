// IndexedDB does not exist in jsdom: this in-memory implementation makes
// local tracking, export and import testable — the only thing browser mode
// owns, hence the only thing it can lose.
import "fake-indexeddb/auto";
