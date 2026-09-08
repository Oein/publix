/**
 * Parse the contents of a .env file.
 *
 * This exists because people do not type environment variables one at a
 * time — they paste a block out of a file, a password manager, or another
 * host's dashboard. The shapes below are all things that block really
 * arrives in, so accepting them is not leniency for its own sake:
 *
 *   KEY=value              plain
 *   export KEY=value       copied out of a shell profile
 *   KEY: value             copied out of a YAML block
 *   KEY="value"            quoted, including a value containing spaces
 *   KEY="line1\nline2"     an escape a quoted value is expected to mean
 *   KEY="""                a value spanning several lines, until the quote
 *   KEY=value # trailing   an unquoted value's comment is not the value
 *   # comment              ignored
 *
 * Anything else is skipped rather than guessed at.
 */
export function parseDotenv(text) {
  const out = [];
  const lines = String(text ?? '').split(/\r?\n/);

  for (let i = 0; i < lines.length; i++) {
    let line = lines[i].trim();
    if (!line || line.startsWith('#')) continue;
    if (line.startsWith('export ')) line = line.slice(7).trim();

    const eq = line.indexOf('=');
    const colon = line.indexOf(':');
    // Prefer '=' — a YAML-ish "KEY: value" only counts when there is no
    // '=' before the colon, or "URL: https://x" would split at the wrong
    // character.
    const at = eq >= 1 && (colon < 0 || eq < colon) ? eq : colon >= 1 ? colon : -1;
    if (at < 1) continue;

    const key = line.slice(0, at).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) continue;

    let rest = line.slice(at + 1).trim();
    const quote = rest[0] === '"' || rest[0] === "'" ? rest[0] : '';

    if (quote) {
      rest = rest.slice(1);
      const close = rest.lastIndexOf(quote);
      if (close >= 0) {
        rest = rest.slice(0, close);
      } else {
        // An unterminated quote opens a value that runs to the closing
        // quote on a later line. Certificates and private keys arrive
        // this way and are worth not mangling.
        const parts = [rest];
        while (++i < lines.length) {
          const next = lines[i];
          const end = next.lastIndexOf(quote);
          if (end >= 0) {
            parts.push(next.slice(0, end));
            break;
          }
          parts.push(next);
        }
        rest = parts.join('\n');
      }
      if (quote === '"') rest = rest.replace(/\\n/g, '\n').replace(/\\"/g, '"');
    } else {
      const hash = rest.indexOf(' #');
      if (hash >= 0) rest = rest.slice(0, hash).trim();
    }

    out.push({ key, value: rest });
  }

  // Later wins, the way sourcing a file twice would.
  const byKey = new Map();
  for (const row of out) byKey.set(row.key, row);
  return [...byKey.values()];
}

/**
 * Whether a pasted string looks like a .env file rather than a single
 * value someone meant to type into the box they were focused on.
 */
export function looksLikeDotenv(text) {
  return parseDotenv(text).length > 0 && /[\r\n]|^\s*(export\s+)?[A-Za-z_][A-Za-z0-9_]*\s*=/.test(text);
}
