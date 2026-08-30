<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';

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
      notify.error('Nothing that looked like KEY=value was found.');
      return;
    }
    const byKey = new Map(rows.map((r) => [r.key, r]));
    for (const row of parsed) byKey.set(row.key, row);
    rows = [...byKey.values()];
    bulk = '';
    showBulk = false;
    notify.info(`${parsed.length} variable${parsed.length === 1 ? '' : 's'} staged — press Save to apply.`);
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
      notify.success('Environment saved. Redeploy for it to take effect.');
      onchange();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

<Card
  title="Environment variables"
  description="Injected into every container. These override anything set in deployment.yaml."
>
  {#snippet actions()}
    <Button size="sm" variant="ghost" onclick={() => (showBulk = !showBulk)}>Paste .env</Button>
    <Button size="sm" onclick={add}>Add variable</Button>
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
        <Button size="sm" variant="primary" onclick={applyBulk}>Add these</Button>
        <Button size="sm" variant="ghost" onclick={() => (showBulk = false)}>Cancel</Button>
        <span class="small faint">Pasted values are marked secret.</span>
      </div>
    </div>
  {/if}

  {#if rows.length === 0}
    <p class="muted small">
      No variables yet. publix always provides <code>PORT</code>, <code>PUBLIX_COMMIT</code> and a
      few others; anything your app needs beyond that goes here.
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
            placeholder={row.stored && !row.dirty ? '•••••••• (unchanged)' : 'value'}
            value={row.stored && !row.dirty ? '' : row.value}
            oninput={(e) => touch(i, { value: e.currentTarget.value, stored: false })}
            autocomplete="off"
            spellcheck="false"
          />
          <label class="secret" title="Hide this value from the dashboard after saving">
            <input
              type="checkbox"
              checked={row.secret}
              onchange={(e) => touch(i, { secret: e.currentTarget.checked })}
            />
            secret
          </label>
          <button class="del" onclick={() => remove(i)} aria-label="Remove {row.key}">×</button>
        </div>
      {/each}
    </div>
  {/if}

  {#if changed}
    <div class="save">
      <Button variant="primary" pending={saving} onclick={save}>Save changes</Button>
      <Button variant="ghost" onclick={reset}>Discard</Button>
      <span class="small muted">Changes apply on the next deployment.</span>
    </div>
  {/if}
</Card>

<Card title="Referencing these in deployment.yaml">
  <p class="muted small">
    A repository can read these without hard-coding values, which keeps credentials out of git:
  </p>
  <pre class="mono">{`env:
  DATABASE_URL: \${secret.DATABASE_URL}
  PUBLIC_URL: https://\${publix.PROJECT}.example.com
  LOG_LEVEL: \${secret.LOG_LEVEL:-info}`}</pre>
  <p class="muted small">
    A reference with no value and no <code>:-default</code> fails the deploy rather than starting
    your app with an empty setting.
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
