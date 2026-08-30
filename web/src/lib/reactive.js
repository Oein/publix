// The two store primitives the dashboard uses.
//
// Svelte 5's runes cover component state; these cover the handful of values
// that live outside any component — the current route and the toast queue.

export function writable(initial) {
  let value = initial;
  const subscribers = new Set();

  return {
    subscribe(fn) {
      subscribers.add(fn);
      fn(value);
      return () => subscribers.delete(fn);
    },
    set(next) {
      value = next;
      for (const fn of subscribers) fn(value);
    },
    update(fn) {
      this.set(fn(value));
    },
    get() {
      return value;
    },
  };
}

export function readable(initial, start) {
  const store = writable(initial);
  let stop = null;
  let count = 0;

  return {
    subscribe(fn) {
      count += 1;
      if (count === 1) stop = start(store.set.bind(store));
      const unsubscribe = store.subscribe(fn);
      return () => {
        unsubscribe();
        count -= 1;
        if (count === 0 && stop) {
          stop();
          stop = null;
        }
      };
    },
    get: store.get,
  };
}
