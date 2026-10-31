<script>
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import { notify } from './toast.js';
  import { t } from './i18n.svelte.js';

  /**
   * Rows from a database, as the explorer returns them: column names and
   * rows of JSON values. A value is shown by its type — NULL apart from an
   * empty string, numbers in a fixed font, documents as compact JSON — and
   * clicking any cell opens it whole.
   */
  let { columns = [], rows = [], offset = 0 } = $props();

  let open = $state(null);

  // MongoDB's extended JSON wraps an ObjectId or a date in an object of one
  // key. Shown as written that is mostly punctuation; show the value.
  function unwrap(v) {
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      const keys = Object.keys(v);
      if (keys.length === 1 && (keys[0] === '$oid' || keys[0] === '$date') && typeof v[keys[0]] !== 'object') return v[keys[0]];
    }
    return v;
  }

  function kind(v) {
    v = unwrap(v);
    if (v === null || v === undefined) return 'null';
    if (Array.isArray(v) || typeof v === 'object') return 'json';
    return typeof v;
  }

  function short(v) {
    v = unwrap(v);
    const k = kind(v);
    if (k === 'null') return 'NULL';
    if (k === 'json') return JSON.stringify(v);
    if (k === 'string' && v === '') return '""';
    return String(v);
  }

  function full(v) {
    if (kind(v) === 'json') return JSON.stringify(v, null, 2);
    if (kind(v) === 'string') {
      // A JSON document stored as text reads better pretty-printed.
      const s = v.trim();
      if ((s.startsWith('{') && s.endsWith('}')) || (s.startsWith('[') && s.endsWith(']'))) {
        try {
          return JSON.stringify(JSON.parse(s), null, 2);
        } catch {
          // Not JSON after all; show it as written.
        }
      }
    }
    return short(v);
  }

  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text);
      notify.success(t('explore.copied'));
    } catch (err) {
      notify.error(err);
    }
  }
</script>

<div class="grid-scroll">
  <table class="grid">
    <thead>
      <tr>
        <th class="rownum">#</th>
        {#each columns as c}<th title={c}>{c}</th>{/each}
      </tr>
    </thead>
    <tbody>
      {#each rows as row, i}
        <tr>
          <td class="rownum">{offset + i + 1}</td>
          {#each row as v, j}
            <td class={kind(v)}>
              <button type="button" onclick={() => (open = { column: columns[j] ?? `#${j + 1}`, value: v, row: offset + i + 1 })}>
                {short(v)}
              </button>
            </td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
</div>

{#if open}
  <Modal title={t('explore.cellTitle', { column: open.column, row: open.row })} wide onclose={() => (open = null)}>
    <pre class="value mono">{full(open.value)}</pre>
    {#snippet footer()}
      <Button onclick={() => copy(full(open.value))}>{t('common.copy')}</Button>
      <Button variant="primary" onclick={() => (open = null)}>{t('common.close')}</Button>
    {/snippet}
  </Modal>
{/if}

<style>
  .grid-scroll {
    overflow: auto;
    max-height: 62vh;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-raised);
  }
  .grid { border-collapse: collapse; font-size: 12.5px; min-width: 100%; }
  th {
    position: sticky; top: 0; z-index: 1;
    background: var(--bg-sunken); color: var(--text-muted);
    font-weight: 600; text-align: left; white-space: nowrap;
    padding: 6px 10px; border-bottom: 1px solid var(--border);
    max-width: 280px; overflow: hidden; text-overflow: ellipsis;
  }
  td { border-bottom: 1px solid var(--border); padding: 0; max-width: 320px; }
  tr:last-child td { border-bottom: none; }
  tbody tr:hover td { background: var(--bg-hover); }
  td button {
    all: unset; box-sizing: border-box; display: block; width: 100%;
    padding: 5px 10px; cursor: pointer;
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  td button:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .rownum {
    color: var(--text-faint, var(--text-muted)); font-family: var(--mono);
    text-align: right; padding: 5px 8px; width: 1%; white-space: nowrap;
    position: sticky; left: 0; background: var(--bg-raised);
  }
  th.rownum { background: var(--bg-sunken); z-index: 2; }
  .number, .boolean { font-family: var(--mono); }
  .number button { text-align: right; }
  .null button { color: var(--text-muted); font-style: italic; font-size: 11.5px; }
  .json button { font-family: var(--mono); color: var(--text-muted); }
  .value {
    margin: 0; padding: 12px 14px; max-height: 60vh; overflow: auto;
    background: var(--bg-sunken); border: 1px solid var(--border); border-radius: var(--radius-sm);
    font-size: 12.5px; line-height: 1.55; white-space: pre-wrap; overflow-wrap: anywhere;
  }
</style>
