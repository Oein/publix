// Translation.
//
// A tiny lookup rather than a library: the dashboard has a few hundred
// strings and no plural rules more complicated than English's, so an i18n
// runtime would be more bytes than the catalogues it loads.
//
// Both catalogues are bundled. They are small, and a dashboard that has to
// fetch a file before it can render its own language would show English
// first and then flicker.

import en from './locales/en.js';
import ko from './locales/ko.js';

const CATALOGS = { en, ko };

/** The selectable languages, named in themselves — nobody looks for "Korean". */
export const LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'ko', label: '한국어' },
];

const KEY = 'publix.locale';

function load() {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved && CATALOGS[saved]) return saved;
  } catch {
    // Storage disabled. Fall through to the browser's preference.
  }
  for (const tag of navigator.languages ?? [navigator.language ?? 'en']) {
    const base = String(tag).toLowerCase().split('-')[0];
    if (CATALOGS[base]) return base;
  }
  return 'en';
}

let current = $state(load());

/** The active language code. Reading it in a component tracks it. */
export function locale() {
  return current;
}

export function setLocale(code) {
  if (!CATALOGS[code]) return;
  current = code;
  document.documentElement.lang = code;
  try {
    localStorage.setItem(KEY, code);
  } catch {
    // The choice still applies for this session.
  }
}

/** Applies the stored language to <html lang>. Call once at startup. */
export function initLocale() {
  document.documentElement.lang = current;
}

function interpolate(template, params) {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (whole, name) =>
    name in params ? String(params[name]) : whole,
  );
}

/**
 * Looks up a string.
 *
 * A missing key falls back to English rather than rendering blank, so a
 * gap in a translation degrades to a readable screen instead of a broken
 * one. A key missing from both is returned as-is, which makes the mistake
 * obvious on screen rather than silent.
 */
export function t(key, params) {
  const entry = CATALOGS[current]?.[key] ?? en[key];
  if (entry === undefined) return key;

  // A plural entry is an object; which form applies is the catalogue's
  // business, since not every language splits them where English does.
  const template =
    typeof entry === 'object'
      ? (entry[params?.count === 1 ? 'one' : 'other'] ?? entry.other)
      : entry;

  return interpolate(template, params);
}

/**
 * Splits a string around its placeholders so a component can render markup
 * where the words are.
 *
 * The alternative — putting `<code>` into the catalogue and rendering it as
 * HTML — would mean injecting project names and volume names straight into
 * the DOM, so this returns segments instead and lets the template build the
 * elements. Placeholders present in `params` are substituted as text; the
 * rest come back as slots for the caller to fill.
 *
 * Returns entries of `{ text }` or `{ slot }`.
 */
export function tparts(key, params) {
  const entry = CATALOGS[current]?.[key] ?? en[key];
  const template = typeof entry === 'object' ? entry.other : (entry ?? key);

  const parts = [];
  let last = 0;
  for (const match of template.matchAll(/\{(\w+)\}/g)) {
    if (params && match[1] in params) continue;
    if (match.index > last) {
      parts.push({
        text: interpolate(template.slice(last, match.index), params),
      });
    }
    parts.push({ slot: match[1] });
    last = match.index + match[0].length;
  }
  if (last < template.length) {
    parts.push({ text: interpolate(template.slice(last), params) });
  }
  return parts;
}
