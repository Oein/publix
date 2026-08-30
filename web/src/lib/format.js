// Small formatting helpers shared across the dashboard. Kept together so
// that dates and sizes read the same way on every screen.

/** Renders a timestamp the way a person reads it: relative and coarse. */
export function age(value) {
  if (!value) return '—';
  const then = new Date(value).getTime();
  if (Number.isNaN(then)) return '—';

  const seconds = Math.round((Date.now() - then) / 1000);
  if (seconds < 0) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(value).toLocaleDateString();
}

/** Full timestamp, for tooltips where precision matters. */
export function exact(value) {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
}

/** Duration between two timestamps, or from one to now. */
export function duration(from, to) {
  if (!from) return '—';
  const start = new Date(from).getTime();
  const end = to ? new Date(to).getTime() : Date.now();
  const ms = end - start;
  if (ms < 0) return '—';
  if (ms < 1000) return `${ms}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

/** Byte count in the largest unit that stays readable. */
export function bytes(n) {
  if (!n) return '—';
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

export function statusLabel(status) {
  switch (status) {
    case 'live':
      return 'Live';
    case 'failed':
      return 'Failed';
    case 'building':
      return 'Building';
    case 'deploying':
      return 'Deploying';
    case 'queued':
      return 'Queued';
    case 'superseded':
      return 'Superseded';
    case 'cancelled':
      return 'Cancelled';
    default:
      return status ?? 'Unknown';
  }
}

/** True while a deployment is still doing something. */
export const isActive = (status) =>
  status === 'queued' || status === 'building' || status === 'deploying';
