import test from 'node:test';
import assert from 'node:assert/strict';
import { parseDotenv, looksLikeDotenv } from './dotenv.js';

/** Convenience: the parsed rows as a plain object. */
function parsed(text) {
  return Object.fromEntries(parseDotenv(text).map((r) => [r.key, r.value]));
}

test('the shapes a .env is actually pasted in', () => {
  assert.deepEqual(
    parsed(
      [
        '# a comment',
        '',
        'PLAIN=value',
        'export EXPORTED=from-a-profile',
        'QUOTED="with spaces"',
        "SINGLE='single quoted'",
        'YAMLISH: from-a-yaml-block',
        'URL: https://example.com/path',
        'TRAILING=value # not part of it',
        'EMPTY=',
        'not a variable at all',
        'lowercase_ok=yes',
      ].join('\n')
    ),
    {
      PLAIN: 'value',
      EXPORTED: 'from-a-profile',
      QUOTED: 'with spaces',
      SINGLE: 'single quoted',
      YAMLISH: 'from-a-yaml-block',
      URL: 'https://example.com/path',
      TRAILING: 'value',
      EMPTY: '',
      lowercase_ok: 'yes',
    }
  );
});

// A '#' inside a quoted value is part of the value. Passwords contain them.
test('a quoted value keeps its hash', () => {
  assert.deepEqual(parsed('PASSWORD="a#b c"'), { PASSWORD: 'a#b c' });
});

// The one that matters for keys and certificates: losing these newlines
// produces a value that fails at runtime with nothing to point at.
test('a double-quoted \\n becomes a newline', () => {
  assert.deepEqual(parsed('PEM="line1\\nline2"'), { PEM: 'line1\nline2' });
});

test('a quote left open runs to the closing quote', () => {
  const rows = parsed(
    ['KEY="-----BEGIN KEY-----', 'abc', 'def', '-----END KEY-----"', 'AFTER=still-read'].join('\n')
  );
  assert.equal(rows.KEY, '-----BEGIN KEY-----\nabc\ndef\n-----END KEY-----');
  assert.equal(rows.AFTER, 'still-read');
});

test('a repeated key takes the last value, the way sourcing twice would', () => {
  assert.deepEqual(parsed('A=1\nA=2'), { A: '2' });
});

test('a name that is not a name is skipped rather than guessed at', () => {
  assert.deepEqual(parsed('1BAD=x\nWITH-DASH=x\nGOOD=x'), { GOOD: 'x' });
});

test('looksLikeDotenv tells a pasted file from a pasted value', () => {
  assert.equal(looksLikeDotenv('A=1\nB=2'), true);
  assert.equal(looksLikeDotenv('DATABASE_URL=postgres://x'), true);
  assert.equal(looksLikeDotenv('just-a-value'), false);
  assert.equal(looksLikeDotenv('postgres://user:pass@host/db'), false);
});
