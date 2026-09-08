<script>
  import { parseDotenv, looksLikeDotenv } from './dotenv.js';
  import Button from './Button.svelte';
  import { t } from './i18n.svelte.js';

  /**
   * A list of environment variables being written from scratch.
   *
   * Nobody types these one key at a time — they paste a block out of a
   * .env file. So pasting is the primary way in: into the paste box, or
   * straight into an empty key field, which is where a paste lands when
   * someone does not notice the box.
   */
  let { rows = $bindable([]) } = $props();

  let bulk = $state('');
  let note = $state('');

  function add() {
    rows = [...rows, { key: '', value: '', secret: true }];
  }

  function remove(index) {
    rows = rows.filter((_, i) => i !== index);
  }

  function set(index, patch) {
    rows = rows.map((r, i) => (i === index ? { ...r, ...patch } : r));
  }

  /** Merge parsed variables in, letting the new value win on a repeat. */
  function merge(parsed) {
    const byKey = new Map(rows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r]));
    for (const { key, value } of parsed) byKey.set(key, { key, value, secret: true });
    rows = [...byKey.values()];
    note = t('env.added', { count: parsed.length });
  }

  function applyBulk() {
    const parsed = parseDotenv(bulk);
    if (parsed.length === 0) {
      note = t('env.nothingParsed');
      return;
    }
    merge(parsed);
    bulk = '';
  }

  /**
   * A .env pasted into a key box is a .env, not a key. Taking it as one
   * saves the "that is not a valid name" round trip.
   */
  function onKeyPaste(event, index) {
    const text = event.clipboardData?.getData('text') ?? '';
    if (!looksLikeDotenv(text)) return;
    event.preventDefault();
    const parsed = parseDotenv(text);
    if (rows[index] && !rows[index].key.trim() && !rows[index].value.trim()) remove(index);
    merge(parsed);
  }
</script>

<div class="env">
  {#if rows.length > 0}
    <div class="rows">
      {#each rows as row, i (i)}
        <div class="row">
          <input
            class="key"
            value={row.key}
            oninput={(e) => set(i, { key: e.currentTarget.value })}
            onpaste={(e) => onKeyPaste(e, i)}
            placeholder="KEY"
            autocomplete="off"
            spellcheck="false"
          />
          <!-- An <input> silently drops newlines, so a pasted certificate
               or private key would arrive as one run-on line and fail at
               runtime with nothing to point at. Multi-line values get a
               box that can hold them. -->
          {#if row.value.includes('\n')}
            <textarea
              class="val"
              rows={Math.min(6, row.value.split('\n').length)}
              value={row.value}
              oninput={(e) => set(i, { value: e.currentTarget.value })}
              spellcheck="false"
            ></textarea>
          {:else}
            <input
              class="val"
              value={row.value}
              oninput={(e) => set(i, { value: e.currentTarget.value })}
              placeholder={t('env.value')}
              autocomplete="off"
              spellcheck="false"
            />
          {/if}
          <label class="secret small muted" title={t('env.secretHint')}>
            <input
              type="checkbox"
              checked={row.secret}
              onchange={(e) => set(i, { secret: e.currentTarget.checked })}
            />
            {t('env.secret')}
          </label>
          <button
            type="button"
            class="x"
            aria-label={t('common.remove', { name: row.key || 'variable' })}
            onclick={() => remove(i)}>×</button
          >
        </div>
      {/each}
    </div>
  {/if}

  <textarea
    bind:value={bulk}
    rows={rows.length ? 3 : 4}
    spellcheck="false"
    placeholder={'KEY=value\nDATABASE_URL=postgres://…'}
    onpaste={() => queueMicrotask(applyBulk)}
  ></textarea>

  <div class="actions">
    <Button size="sm" onclick={add}>{t('env.addVariable')}</Button>
    {#if bulk.trim()}
      <Button size="sm" variant="primary" onclick={applyBulk}>{t('env.addThese')}</Button>
    {/if}
    <span class="grow"></span>
    <span class="small muted">{note || t('env.pastedSecret')}</span>
  </div>
</div>

<style>
  .env { display: flex; flex-direction: column; gap: 9px; }
  .rows { display: flex; flex-direction: column; gap: 6px; }

  .row { display: flex; align-items: center; gap: 6px; }
  .row textarea.val {
    flex: 1;
    min-width: 0;
    font-family: var(--mono);
    font-size: 12px;
    line-height: 1.45;
    padding: 5px 7px;
    resize: vertical;
  }
  .row input.key { flex: 0 0 34%; font-family: var(--mono); font-size: 12px; }
  .row input.val { flex: 1; min-width: 0; font-family: var(--mono); font-size: 12px; }

  .secret {
    display: flex;
    align-items: center;
    gap: 4px;
    flex: none;
    white-space: nowrap;
    cursor: pointer;
  }

  .x {
    flex: none;
    width: 24px;
    height: 24px;
    border: 0;
    background: none;
    color: var(--text-muted);
    font-size: 17px;
    line-height: 1;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .x:hover { background: var(--bad-bg); color: var(--bad); }

  textarea {
    font-family: var(--mono);
    font-size: 12px;
    line-height: 1.5;
    resize: vertical;
  }

  .actions { display: flex; align-items: center; gap: 8px; }
  .grow { flex: 1; }
</style>
