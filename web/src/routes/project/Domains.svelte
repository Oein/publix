<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import { t } from '../../lib/i18n.svelte.js';

  let { project, onchange } = $props();

  let domains = $state([]);
  let draft = $state('');
  let saving = $state(false);

  function reset() {
    domains = [...(project.domains ?? [])];
    draft = '';
  }

  $effect(() => {
    project.id;
    project.updatedAt;
    reset();
  });

  const changed = $derived(
    JSON.stringify(domains) !== JSON.stringify(project.domains ?? [])
  );

  // The hostname publix generates is not editable here — it is derived from
  // the slug and the server's apps domain — so it is shown separately.
  const generated = $derived(
    (project.hosts ?? []).filter((h) => !domains.includes(h))
  );

  function add() {
    const value = draft.trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, '');
    if (!value) return;
    if (!value.includes('.')) {
      notify.error(t('domains.notHostname', { value }));
      return;
    }
    if (domains.includes(value)) {
      notify.error(t('domains.alreadyListed', { value }));
      return;
    }
    domains = [...domains, value];
    draft = '';
  }

  async function save() {
    saving = true;
    try {
      await api.projects.setDomains(project.id, domains);
      notify.success(t('domains.updated'));
      onchange();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

<Card title={t('domains.title')} description={t('domains.desc')}>
  <div class="add">
    <input
      bind:value={draft}
      placeholder="app.example.com"
      autocomplete="off"
      spellcheck="false"
      onkeydown={(e) => e.key === 'Enter' && (e.preventDefault(), add())}
    />
    <Button onclick={add}>{t('common.add')}</Button>
  </div>

  {#if domains.length === 0}
    <p class="muted small">{t('domains.emptyNote')}</p>
  {:else}
    <ul>
      {#each domains as domain (domain)}
        <li>
          <a href="https://{domain}" target="_blank" rel="noreferrer noopener" class="mono">
            {domain} ↗
          </a>
          <button
            class="del"
            onclick={() => (domains = domains.filter((d) => d !== domain))}
            aria-label={t('common.remove', { name: domain })}
          >×</button>
        </li>
      {/each}
    </ul>
  {/if}

  {#if changed}
    <div class="save">
      <Button variant="primary" pending={saving} onclick={save}>{t('domains.save')}</Button>
      <Button variant="ghost" onclick={reset}>{t('common.discard')}</Button>
      <span class="small muted">{t('domains.immediate')}</span>
    </div>
  {/if}
</Card>

{#if generated.length > 0}
  <Card title={t('domains.generatedTitle')}>
    <ul>
      {#each generated as host}
        <li>
          <a href="https://{host}" target="_blank" rel="noreferrer noopener" class="mono">
            {host} ↗
          </a>
        </li>
      {/each}
    </ul>
    <p class="muted small note">{t('domains.generatedNote')}</p>
  </Card>
{/if}

<Card title={t('domains.specTitle')}>
  <p class="muted small">{t('domains.specBlurb')}</p>
  <pre class="mono">{`domains:
  - app.example.com

routes:
  - domain: www.example.com
    redirectTo: app.example.com
  - domain: example.com
    path: /api
    stripPath: true`}</pre>
</Card>

<style>
  .add { display: flex; gap: 8px; margin-bottom: 12px; }
  .add input {
    flex: 1;
    padding: 6px 9px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 13px;
    font-family: var(--mono);
  }
  .add input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }

  ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 6px 10px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
  }

  .del {
    border: none;
    background: none;
    font-size: 17px;
    line-height: 1;
    color: var(--text-faint);
    cursor: pointer;
    padding: 0 4px;
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

  .note { margin-top: 10px; line-height: 1.55; }

  pre {
    margin: 8px 0 0;
    padding: 11px 13px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
    line-height: 1.6;
    overflow-x: auto;
  }
</style>
