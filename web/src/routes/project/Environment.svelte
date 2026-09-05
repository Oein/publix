<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import { t, tparts } from '../../lib/i18n.svelte.js';

  /**
   * Environment variables.
   *
   * A secret's value is never sent to the browser, so an existing secret
   * shows as a placeholder rather than a blank box — the difference between
   * "there is a value here you cannot see" and "this is empty" matters when
   * you are about to press Save.
   */
  let { project, onchange } = $props();

  let rows = $state([]);
  let saving = $state(false);
  let bulk = $state('');
  let showBulk = $state(false);

  function reset() {
    rows = (project.env ?? []).map((e) => ({
      key: e.key,
      value: e.value,
      secret: e.secret,
      stored: e.secret && !e.value,
      dirty: false,
    }));
  }

  $effect(() => {
    project.id;
    project.updatedAt;
    reset();
  });

  const changed = $derived(
    rows.some((r) => r.dirty) || rows.length !== (project.env ?? []).length
  );

  function add() {
    rows = [...rows, { key: '', value: '', secret: false, stored: false, dirty: true }];
  }

  function remove(index) {
    rows = rows.filter((_, i) => i !== index);
  }

  function touch(index, patch) {
    rows = rows.map((r, i) => (i === index ? { ...r, ...patch, dirty: true } : r));
  }

  function applyBulk() {
    const parsed = [];
    for (const raw of bulk.split('\n')) {
      const line = raw.trim();
      if (!line || line.startsWith('#')) continue;
      const eq = line.indexOf('=');
      if (eq < 1) continue;
      let value = line.slice(eq + 1).trim();
      // Accept the quoting people paste out of a .env file.
      if (
        (value.startsWith('"') && value.endsWith('"')) ||
        (value.startsWith("'") && value.endsWith("'"))
      ) {
        value = value.slice(1, -1);
      }
      parsed.push({ key: line.slice(0, eq).trim(), value, secret: true, stored: false, dirty: true });
    }
    if (parsed.length === 0) {
      notify.error(t('env.nothingParsed'));
      return;
    }
    const byKey = new Map(rows.map((r) => [r.key, r]));
    for (const row of parsed) byKey.set(row.key, row);
    rows = [...byKey.values()];
    bulk = '';
    showBulk = false;
    notify.info(t('env.staged', { count: parsed.length }));
  }

  async function save() {
    saving = true;
    try {
      await api.projects.setEnv(
        project.id,
        // A secret left untouched sends an empty value, which the server
        // reads as "keep what you already have".
        rows
          .filter((r) => r.key.trim())
          .map((r) => ({
            key: r.key.trim(),
            value: r.stored && !r.dirty ? '' : r.value,
            secret: r.secret,
          }))
      );
      notify.success(t('env.saved'));
      onchange();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

<Card title={t('env.title')} description={t('env.desc')}>
  {#snippet actions()}
    <Button size="sm" variant="ghost" onclick={() => (showBulk = !showBulk)}>
      {t('env.paste')}
    </Button>
    <Button size="sm" onclick={add}>{t('env.addVariable')}</Button>
  {/snippet}

  {#if showBulk}
    <div class="bulk">
      <textarea
        bind:value={bulk}
        rows="6"
        spellcheck="false"
        placeholder={'DATABASE_URL=postgres://…\nAPI_KEY=…'}
      ></textarea>
      <div class="row">
        <Button size="sm" variant="primary" onclick={applyBulk}>{t('env.addThese')}</Button>
        <Button size="sm" variant="ghost" onclick={() => (showBulk = false)}>
          {t('common.cancel')}
        </Button>
        <span class="small faint">{t('env.pastedSecret')}</span>
      </div>
    </div>
  {/if}

  {#if rows.length === 0}
    <p class="muted small">
      {#each tparts('env.emptyNote') as part}{#if part.slot === 'port'}<code>PORT</code
        >{:else if part.slot === 'commit'}<code>PUBLIX_COMMIT</code
        >{:else}{part.text}{/if}{/each}
    </p>
  {:else}
    <div class="rows">
      {#each rows as row, i (i)}
        <div class="var">
          <input
            class="key mono"
            placeholder="KEY"
            value={row.key}
            oninput={(e) => touch(i, { key: e.currentTarget.value })}
            autocomplete="off"
            spellcheck="false"
          />
          <input
            class="value mono"
            type={row.secret && !row.dirty && row.stored ? 'password' : 'text'}
            placeholder={row.stored && !row.dirty ? t('env.unchanged') : t('env.value')}
            value={row.stored && !row.dirty ? '' : row.value}
            oninput={(e) => touch(i, { value: e.currentTarget.value, stored: false })}
            autocomplete="off"
            spellcheck="false"
          />
          <label class="secret" title={t('env.secretHint')}>
            <input
              type="checkbox"
              checked={row.secret}
              onchange={(e) => touch(i, { secret: e.currentTarget.checked })}
            />
            {t('env.secret')}
          </label>
          <button
            class="del"
            onclick={() => remove(i)}
            aria-label={t('common.remove', { name: row.key })}>×</button
          >
        </div>
      {/each}
    </div>
  {/if}

  {#if changed}
    <div class="save">
      <Button variant="primary" pending={saving} onclick={save}>{t('common.saveChanges')}</Button>
      <Button variant="ghost" onclick={reset}>{t('common.discard')}</Button>
      <span class="small muted">{t('env.applyNext')}</span>
    </div>
  {/if}
</Card>

<Card title={t('env.refTitle')}>
  <p class="muted small">{t('env.refBlurb')}</p>
  <pre class="mono">{`env:
  DATABASE_URL: \${secret.DATABASE_URL}
  PUBLIC_URL: https://\${publix.PROJECT}.example.com
  LOG_LEVEL: \${secret.LOG_LEVEL:-info}`}</pre>
  <p class="muted small">
    {#each tparts('env.refNote') as part}{#if part.slot}<code>:-default</code
      >{:else}{part.text}{/if}{/each}
  </p>
</Card>

<style>
  .rows { display: flex; flex-direction: column; gap: 6px; }

  .var {
    display: grid;
    grid-template-columns: minmax(140px, 1fr) minmax(180px, 2fr) auto auto;
    gap: 6px;
    align-items: center;
  }
  @media (max-width: 640px) {
    .var { grid-template-columns: 1fr 1fr; }
  }

  input.key, input.value {
    padding: 5px 8px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
    min-width: 0;
  }
  input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-bg); }

  .secret {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    color: var(--text-muted);
    white-space: nowrap;
    cursor: pointer;
  }

  .del {
    border: none;
    background: none;
    font-size: 17px;
    line-height: 1;
    color: var(--text-faint);
    cursor: pointer;
    padding: 0 5px;
    border-radius: 4px;
  }
  .del:hover { color: var(--bad); background: var(--bad-bg); }

  .save {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }

  .bulk { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
  textarea {
    width: 100%;
    padding: 9px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-family: var(--mono);
    font-size: 12.5px;
    resize: vertical;
  }

  pre {
    margin: 8px 0;
    padding: 11px 13px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
    line-height: 1.6;
    overflow-x: auto;
  }

  code {
    padding: 1px 4px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }
</style>
