<script>
  import { api } from '../lib/api.js';
  import { age } from '../lib/format.js';
  import { notify } from '../lib/toast.js';
  import { navigate } from '../lib/router.js';
  import Badge from '../lib/Badge.svelte';
  import Button from '../lib/Button.svelte';
  import Empty from '../lib/Empty.svelte';
  import ImportDialog from './ImportDialog.svelte';
  import { t, tparts } from '../lib/i18n.svelte.js';

  let { revision } = $props();

  // Declared before the state that uses them: a `let x = $state(PAGE)` above
  // its own `const PAGE` is a temporal dead zone, and the whole screen
  // renders blank.
  const PAGE = 30;
  const REMEMBER = 'publix.github.account';

  let status = $state(null);
  let repos = $state(null);
  let query = $state('');
  let selected = $state(null);
  // The account whose repositories are listed. An App can be installed on
  // several, and reading them all to draw one screen cost a request per
  // account before anything appeared.
  let account = $state(remembered());
  // How many rows are rendered. Everything is loaded; this is what keeps a
  // thousand repositories from becoming a thousand DOM nodes at once.
  let visible = $state(PAGE);
  let sentinel = $state(null);

  function remembered() {
    try {
      return localStorage.getItem(REMEMBER) ?? '';
    } catch {
      return '';
    }
  }

  const accounts = $derived(status?.installations?.map((i) => i.login) ?? []);

  async function loadStatus() {
    try {
      status = await api.github.status();
    } catch (err) {
      notify.error(err);
      status = { configured: false };
    }
  }

  async function loadRepos() {
    if (!status?.configured) return;
    repos = null;
    visible = PAGE;
    try {
      repos = await api.github.repos(account);
    } catch (err) {
      notify.error(err);
      repos = [];
    }
  }

  function pick(next) {
    account = next;
    try {
      localStorage.setItem(REMEMBER, next);
    } catch {
      // A browser that refuses storage still gets the switch, just not
      // the memory of it.
    }
    loadRepos();
  }

  $effect(() => {
    revision;
    loadStatus();
  });

  // Settle on an account before the first load: a remembered one that is
  // still installed, otherwise the first.
  $effect(() => {
    if (!status?.configured) return;
    if (accounts.length && !accounts.includes(account)) account = accounts[0];
  });

  $effect(() => {
    if (!status?.configured) return;
    account;
    loadRepos();
  });

  // Reset the window whenever the visible set changes underneath it, or a
  // search would open onto page three of the previous one.
  $effect(() => {
    query;
    visible = PAGE;
  });

  // Grow the window while the end of the list is on screen. The observer
  // fires again as soon as the sentinel is still visible after a batch, so
  // a search with no match near the top walks down the list on its own.
  $effect(() => {
    if (!sentinel) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) visible += PAGE;
    });
    io.observe(sentinel);
    return () => io.disconnect();
  });

  const filtered = $derived(
    !repos
      ? []
      : repos.filter((r) => r.full_name.toLowerCase().includes(query.trim().toLowerCase()))
  );
  const shown = $derived(filtered.slice(0, visible));
  // "Grant it some repositories" has to point at the account being looked
  // at, not at whichever installation happened to be described first.
  const installationUrl = $derived(
    status?.installations?.find((i) => i.login === account)?.url ?? status?.installationUrl
  );

  function imported(result) {
    selected = null;
    for (const warning of result.warnings ?? []) notify.error(warning);
    notify.success(t('import.imported', { name: result.project.name }));
    navigate(`/projects/${result.project.slug}`);
  }
</script>

<div class="head">
  <h1>{t('import.title')}</h1>
  <p class="muted small">
    {#each tparts('import.blurb') as part}{#if part.slot}<code>deployment.yaml</code
      >{:else}{part.text}{/if}{/each}
  </p>
</div>

{#if status === null}
  <div class="ghost"></div>
{:else if !status.configured}
  <div class="panel">
    <Empty
      title={t('import.notConnectedTitle')}
      description={t('import.notConnectedDesc')}
    >
      <a href="#/settings/github"><Button variant="primary">{t('import.connect')}</Button></a>
    </Empty>
  </div>
{:else if status.error}
  <div class="panel error-panel">
    <strong>{t('import.ghError')}</strong>
    <p class="muted small">{status.error}</p>
    <a href="#/settings/github"><Button size="sm">{t('import.checkSettings')}</Button></a>
  </div>
{:else}
  <div class="toolbar">
    <input
      class="search"
      type="search"
      placeholder={t('import.search')}
      bind:value={query}
      autocomplete="off"
    />
    <!-- An App can be installed on several accounts. Choosing one here is
         what keeps the screen fast: publix reads that account only. -->
    {#if accounts.length > 1}
      <label class="account small muted nowrap">
        {t('import.account')}
        <select value={account} onchange={(e) => pick(e.currentTarget.value)}>
          {#each accounts as login (login)}
            <option value={login}>{login}</option>
          {/each}
        </select>
      </label>
    {:else}
      <span class="small muted nowrap">
        {#if status.login}{t('import.connectedAs')} <strong>{status.login}</strong>{/if}
      </span>
    {/if}
    <Button size="sm" onclick={loadRepos}>{t('common.refresh')}</Button>
  </div>

  {#if repos === null}
    <div class="list">
      {#each Array(5) as _}<div class="ghost row-ghost"></div>{/each}
    </div>
  {:else if repos.length === 0}
    <div class="panel">
      <!-- An App can connect perfectly and still show nothing, because
           connecting and granting repository access are separate steps on
           GitHub's side. Say which one is missing rather than listing both
           possibilities. -->
      {#if status.mode === 'app' && installationUrl}
        <Empty
          title={t('import.noReposAppTitle')}
          description={t('import.noReposAppDesc', { account: account || status.login || '—' })}
        >
          <a href={installationUrl} target="_blank" rel="noreferrer noopener">
            <Button size="sm" variant="primary">{t('import.manageAccess')} ↗</Button>
          </a>
        </Empty>
      {:else}
        <Empty title={t('import.noReposTitle')} description={t('import.noReposDesc')}>
          <a href="#/settings/github"><Button size="sm">{t('import.reviewSettings')}</Button></a>
        </Empty>
      {/if}
    </div>
  {:else if filtered.length === 0}
    <div class="panel">
      <Empty title={t('import.noMatchTitle', { query })} description={t('import.noMatchDesc')} />
    </div>
  {:else}
    <ul class="list">
      {#each shown as repo (repo.id)}
        <li class="repo">
          <div class="grow">
            <div class="row">
              <span class="name truncate">{repo.full_name}</span>
              {#if repo.private}<Badge tone="muted">{t('import.private')}</Badge>{/if}
              {#if repo.archived}<Badge tone="warn">{t('import.archived')}</Badge>{/if}
            </div>
            {#if repo.description}
              <p class="muted small truncate desc">{repo.description}</p>
            {/if}
            <div class="meta small faint">
              {#if repo.language}<span>{repo.language}</span><span>·</span>{/if}
              <span>{repo.default_branch}</span>
              <span>·</span>
              <span>{t('import.updated', { age: age(repo.pushed_at) })}</span>
            </div>
          </div>

          {#if repo.imported}
            <a href="#/projects/{repo.projectId}">
              <Button size="sm">{t('import.openProject')}</Button>
            </a>
          {:else}
            <Button size="sm" variant="primary" onclick={() => (selected = repo)}>
              {t('import.import')}
            </Button>
          {/if}
        </li>
      {/each}
    </ul>
    {#if shown.length < filtered.length}
      <div class="sentinel" bind:this={sentinel}>
        <div class="ghost row-ghost"></div>
      </div>
    {:else if filtered.length > PAGE}
      <p class="small faint end">{t('import.allShown', { count: filtered.length })}</p>
    {/if}
  {/if}
{/if}

{#if selected}
  <!-- Keyed so switching between repositories rebuilds the dialog rather
       than leaving it holding form state from the previous one. -->
  {#key selected.id}
    <ImportDialog repo={selected} onclose={() => (selected = null)} onimported={imported} />
  {/key}
{/if}

<style>
  .head { margin-bottom: 16px; }
  .head p { margin: 4px 0 0; max-width: 68ch; }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-size: 12px;
  }

  .toolbar {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 12px;
  }

  .account { display: flex; align-items: center; gap: 6px; }
  .account select {
    font: inherit;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 4px 6px;
  }
  .sentinel { margin-top: 10px; }
  .end { text-align: center; margin: 12px 0 0; }

  .search {
    flex: 1;
    padding: 7px 11px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 13px;
  }
  .search:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .repo {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 11px 16px;
    border-bottom: 1px solid var(--border);
  }
  .repo:last-child { border-bottom: none; }
  .repo:hover { background: var(--bg-hover); }

  .name { font-weight: 550; font-size: 13.5px; }
  .desc { margin: 2px 0 0; max-width: 70ch; }
  .meta { display: flex; gap: 6px; margin-top: 2px; }

  .panel {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .error-panel {
    padding: 16px;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
    border-left: 3px solid var(--bad);
  }

  .ghost {
    height: 68px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    animation: fade 1.4s ease-in-out infinite;
  }
  .row-ghost { border-radius: 0; border-bottom: none; }
  @keyframes fade { 50% { opacity: 0.55; } }
</style>
