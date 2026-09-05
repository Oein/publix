// Small formatting helpers shared across the dashboard. Kept together so
// that dates and sizes read the same way on every screen.

import { locale, t } from './i18n.svelte.js';

// Intl formatters are expensive to construct and are asked for on every
// row of every list, so keep one per locale.
const relative = new Map();

function rtf(tag) {
  let f = relative.get(tag);
  if (!f) {
    f = new Intl.RelativeTimeFormat(tag, { style: 'narrow' });
    relative.set(tag, f);
  }
  return f;
}

/**
 * Renders a timestamp the way a person reads it: relative and coarse.
 *
 * Intl does the wording, so "3m ago" becomes "3분 전" without this file
 * knowing anything about Korean.
 */
export function age(value) {
  if (!value) return t('fmt.none');
  const then = new Date(value).getTime();
  if (Number.isNaN(then)) return t('fmt.none');

  const tag = locale();
  const seconds = Math.round((Date.now() - then) / 1000);
  if (seconds < 0) return t('fmt.justNow');
  if (seconds < 60) return rtf(tag).format(-seconds, 'second');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return rtf(tag).format(-minutes, 'minute');
  const hours = Math.round(minutes / 60);
  if (hours < 24) return rtf(tag).format(-hours, 'hour');
  const days = Math.round(hours / 24);
  if (days < 30) return rtf(tag).format(-days, 'day');
  return new Date(value).toLocaleDateString(tag);
}

/** Full timestamp, for tooltips where precision matters. */
export function exact(value) {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(locale());
}

/** Duration between two timestamps, or from one to now. */
export function duration(from, to) {
  if (!from) return t('fmt.none');
  const start = new Date(from).getTime();
  const end = to ? new Date(to).getTime() : Date.now();
  const ms = end - start;
  if (ms < 0) return t('fmt.none');
  if (ms < 1000) return t('fmt.durationMs', { ms });
  const s = Math.round(ms / 1000);
  if (s < 60) return t('fmt.durationSeconds', { s });
  const m = Math.floor(s / 60);
  return t('fmt.durationMinutes', { m, s: s % 60 });
}

/** Byte count in the largest unit that stays readable. */
export function bytes(n) {
  if (!n) return t('fmt.none');
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)}${units[i]}`;
}

/** First line of a commit message, which is all a list row has room for. */
export function subject(message) {
  if (!message) return '';
  return message.split('\n')[0];
}

export const shortSha = (sha) => (sha ? sha.slice(0, 7) : '');

/** Maps a deployment status onto the tone used for badges and dots. */
export function statusTone(status) {
  switch (status) {
    case 'live':
      return 'good';
    case 'failed':
      return 'bad';
    case 'building':
    case 'deploying':
    case 'queued':
      return 'busy';
    case 'cancelled':
      return 'warn';
    default:
      return 'muted';
  }
}

const STATUSES = ['live', 'failed', 'building', 'deploying', 'queued', 'superseded', 'cancelled'];

/**
 * A status name in the reader's language.
 *
 * A status the server introduces that this dashboard has not been taught
 * shows through untranslated rather than as "Unknown", which is at least
 * the truth about what the server said.
 */
export function statusLabel(status) {
  if (STATUSES.includes(status)) return t(`status.${status}`);
  return status || t('status.unknown');
}

/** True while a deployment is still doing something. */
export const isActive = (status) =>
  status === 'queued' || status === 'building' || status === 'deploying';
