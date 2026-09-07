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
  let appsDomains = $state([]);
  let appsDomain = $state('');
  let movingParent = $state(false);

  function reset() {
    domains = [...(project.domains ?? [])];
    draft = '';
    appsDomain = project.appsDomain ?? '';
  }

  $effect(() => {
    api.settings
      .get()
      .then((s) => (appsDomains = s.appsDomains ?? []))
      .catch(() => (appsDomains = []));
  });

  const defaultAppsDomain = $derived(
    appsDomains.find((d) => d.default)?.domain ?? appsDomains[0]?.domain ?? ''
  );

  const plannedHost = $derived.by(() => {
    const parent = appsDomain === 'none' ? '' : appsDomain || defaultAppsDomain;
    return parent ? `${project.slug}.${parent}` : '';
  });

  const parentChanged = $derived(appsDomain !== (project.appsDomain ?? ''));

  async function moveParent() {
    movingParent = true;
    try {
      await api.projects.update(project.id, { appsDomain });
      notify.success(
        plannedHost
          ? t('domains.moved', { host: plannedHost })
          : t('domains.movedNone')
      );
      onchange();
    } catch (err) {
      notify.error(err);
    } finally {
      movingParent = false;
    }
  }

  $effect(() => {
    project.id;
    project.updatedAt;
    reset();
  });

  const changed = $derived(
    JSON.stringify(domains) !== JSON.stringify(project.domains ?? [])
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

<Card title={t('domains.generatedTitle')} description={t('domains.generatedDesc')}>
  {#if appsDomains.length === 0}
    <p class="muted small">{t('domains.noAppsDomains')}</p>
    <a href="#/settings/domains"><Button size="sm">{t('domains.manageAppsDomains')}</Button></a>
  {:else}
    <div class="parent">
      <select bind:value={appsDomain} aria-label={t('domains.appsDomain')}>
        <option value="">{t('domains.appsDomainDefault', { domain: defaultAppsDomain })}</option>
        {#each appsDomains as d}
          {#if d.domain !== defaultAppsDomain}
            <option value={d.domain}>{d.domain}</option>
          {/if}
        {/each}
        <option value="none">{t('domains.appsDomainNone')}</option>
      </select>
      {#if parentChanged}
        <Button variant="primary" pending={movingParent} onclick={moveParent}>
          {t('common.save')}
        </Button>
        <Button variant="ghost" onclick={reset}>{t('common.discard')}</Button>
      {/if}
    </div>

    {#if plannedHost}
      <ul>
        <li>
          <a href="https://{plannedHost}" target="_blank" rel="noreferrer noopener" class="mono">
            {plannedHost} ↗
          </a>
        </li>
      </ul>
    {:else}
      <p class="muted small">{t('domains.noGeneratedHost')}</p>
    {/if}
    <p class="muted small note">{t('domains.generatedNote')}</p>
  {/if}
</Card>

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

  .parent {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 10px;
  }
  /* Field styles its own controls; this select sits outside one, so it
     has to say the same thing itself. */
  .parent select {
    max-width: 340px;
    flex: 1;
    padding: 6px 9px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 13px;
    color: var(--text);
  }
  .parent select:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

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
