// A hash router.
//
// Hash routing avoids needing any server-side rewrite rules, which matters
// for a dashboard that people put behind their own reverse proxy in
// configurations publix does not control.

import { readable } from './reactive.js';

function parse() {
  const raw = window.location.hash.replace(/^#/, '') || '/';
  const [path, query] = raw.split('?');
  return {
    path: path || '/',
    segments: path.split('/').filter(Boolean),
    query: new URLSearchParams(query ?? ''),
  };
}

export const route = readable(parse(), (set) => {
  const update = () => set(parse());
  window.addEventListener('hashchange', update);
  return () => window.removeEventListener('hashchange', update);
});

export function navigate(path) {
  if (window.location.hash === `#${path}`) return;
  window.location.hash = path;
}

/** Replaces the current entry, for redirects that should not add history. */
export function replace(path) {
  const url = new URL(window.location.href);
  url.hash = path;
  window.history.replaceState(null, '', url.toString());
  window.dispatchEvent(new HashChangeEvent('hashchange'));
}
