// Theme selection.
//
// Three states rather than two: "system" is a real choice, not the absence
// of one, and a dashboard people leave open all day should follow their
// machine switching at dusk unless they said otherwise.

const KEY = 'publix.theme';

/** The selectable themes, in the order the picker shows them. */
export const THEMES = ['system', 'light', 'dark'];

function load() {
  try {
    const saved = localStorage.getItem(KEY);
    return THEMES.includes(saved) ? saved : 'system';
  } catch {
    // A browser with storage disabled still gets a working dashboard; it
    // just follows the OS and forgets the choice on reload.
    return 'system';
  }
}

let current = $state(load());

export function theme() {
  return current;
}

/**
 * Applies a theme to the document.
 *
 * "system" removes the attribute entirely rather than resolving it here, so
 * the CSS media query stays in charge and the page follows the OS live —
 * writing the resolved value would freeze it at whatever it was on load.
 */
function apply(value) {
  const root = document.documentElement;
  if (value === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', value);
}

export function setTheme(value) {
  if (!THEMES.includes(value)) return;
  current = value;
  apply(value);
  try {
    localStorage.setItem(KEY, value);
  } catch {
    // Same as above: the choice still applies, it just does not survive a
    // reload. Failing the click over it would be worse.
  }
}

/** Applies the stored theme. Call once, as early as possible. */
export function initTheme() {
  apply(current);
}
