// Transient notifications.
//
// Errors stay until dismissed: a deploy failure that vanishes after three
// seconds is worse than no notification at all, because the user knows
// something happened and can no longer find out what.

import { writable } from './reactive.js';

export const toasts = writable([]);

let nextId = 1;

function push(tone, message, details = []) {
  const id = nextId++;
  toasts.update((list) => [...list, { id, tone, message, details }]);
  if (tone !== 'error') {
    setTimeout(() => dismiss(id), 4000);
  }
  return id;
}

export function dismiss(id) {
  toasts.update((list) => list.filter((t) => t.id !== id));
}

export const notify = {
  success: (message) => push('success', message),
  info: (message) => push('info', message),
  error: (err) => {
    if (typeof err === 'string') return push('error', err);
    return push('error', err?.message ?? 'Something went wrong', err?.details ?? []);
  },
};
