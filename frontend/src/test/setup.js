// Test setup. Node 26 ships an experimental global `localStorage` that is
// disabled (undefined) unless `--localstorage-file` is passed, which shadows
// jsdom's. Install a minimal in-memory implementation so the auth/api specs
// have a deterministic store regardless of the runtime quirk.
import { beforeEach } from 'vitest';

class MemoryStorage {
  #map = new Map();
  getItem(k) {
    return this.#map.has(k) ? this.#map.get(k) : null;
  }
  setItem(k, v) {
    this.#map.set(k, String(v));
  }
  removeItem(k) {
    this.#map.delete(k);
  }
  clear() {
    this.#map.clear();
  }
}

const store = new MemoryStorage();
globalThis.localStorage = store;
if (typeof window !== 'undefined') window.localStorage = store;

beforeEach(() => store.clear());
