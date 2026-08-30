<script>
  import { api } from '../lib/api.js';
  import { age } from '../lib/format.js';
  import { notify } from '../lib/toast.js';
  import { navigate } from '../lib/router.js';
  import Badge from '../lib/Badge.svelte';
  import Button from '../lib/Button.svelte';
  import Empty from '../lib/Empty.svelte';
  import ImportDialog from './ImportDialog.svelte';

  let { revision } = $props();

  let status = $state(null);
  let repos = $state(null);
  let query = $state('');
  let selected = $state(null);

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
    try {
      repos = await api.github.repos();
    } catch (err) {
      notify.error(err);
      repos = [];
    }
  }

  $effect(() => {
    revision;
    loadStatus();
  });

  $effect(() => {
    if (status?.configured) loadRepos();
  });

  const filtered = $derived(
    !repos
      ? []
      : repos.filter((r) => r.full_name.toLowerCase().includes(query.trim().toLowerCase()))
  );

  function imported(result) {
    selected = null;
    for (const warning of result.warnings ?? []) notify.error(warning);
    notify.success(`Imported ${result.project.name}`);
    navigate(`/projects/${result.project.slug}`);
  }
</script>

<div class="head">
  <h1>Import a repository</h1>
  <p class="muted small">
    publix reads the repository, works out how to build it, and deploys it. If it has a
    <code>deployment.yaml</code> that is used as-is; otherwise one is suggested for you.
  </p>
</div>

{#if status === null}
  <div class="ghost"></div>
{:else if !status.configured}
  <div class="panel">
    <Empty
      title="GitHub is not connected"
      description="Connect a personal access token or a GitHub App and your repositories will appear here, ready to import in one click."
    >
      <a href="#/settings/github"><Button variant="primary">Connect GitHub</Button></a>
    </Empty>
  </div>
{:else if status.error}
  <div class="panel error-panel">
    <strong>GitHub returned an error</strong>
    <p class="muted small">{status.error}</p>
    <a href="#/settings/github"><Button size="sm">Check GitHub settings</Button></a>
  </div>
{:else}
  <div class="toolbar">
    <input
      class="search"
      type="search"
      placeholder="Search repositories…"
      bind:value={query}
      autocomplete="off"
    />
    <span class="small muted nowrap">
      {#if status.login}Connected as <strong>{status.login}</strong>{/if}
    </span>
    <Button size="sm" onclick={loadRepos}>Refresh</Button>
  </div>

  {#if repos === null}
    <div class="list">
      {#each Array(5) as _}<div class="ghost row-ghost"></div>{/each}
    </div>
  {:else if repos.length === 0}
    <div class="panel">
      <Empty
        title="No repositories found"
        description="The connected credentials cannot see any repositories. A fine-grained token needs explicit repository access; a GitHub App needs to be installed on the account."
      >
        <a href="#/settings/github"><Button size="sm">Review GitHub settings</Button></a>
      </Empty>
    </div>
  {:else if filtered.length === 0}
    <div class="panel">
      <Empty title="Nothing matches “{query}”" description="Try a different search." />
    </div>
  {:else}
    <ul class="list">
      {#each filtered as repo (repo.id)}
        <li class="repo">
          <div class="grow">
            <div class="row">
              <span class="name truncate">{repo.full_name}</span>
              {#if repo.private}<Badge tone="muted">Private</Badge>{/if}
              {#if repo.archived}<Badge tone="warn">Archived</Badge>{/if}
            </div>
            {#if repo.description}
              <p class="muted small truncate desc">{repo.description}</p>
            {/if}
            <div class="meta small faint">
              {#if repo.language}<span>{repo.language}</span><span>·</span>{/if}
              <span>{repo.default_branch}</span>
              <span>·</span>
              <span>updated {age(repo.pushed_at)}</span>
            </div>
          </div>

          {#if repo.imported}
            <a href="#/projects/{repo.projectId}"><Button size="sm">Open project</Button></a>
          {:else}
            <Button size="sm" variant="primary" onclick={() => (selected = repo)}>Import</Button>
          {/if}
        </li>
      {/each}
    </ul>
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
