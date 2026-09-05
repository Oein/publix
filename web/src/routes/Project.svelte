<script>
  import { api } from '../lib/api.js';
  import { navigate } from '../lib/router.js';
  import { notify } from '../lib/toast.js';
  import { statusTone, statusLabel, age, subject } from '../lib/format.js';
  import Badge from '../lib/Badge.svelte';
  import Button from '../lib/Button.svelte';
  import Confirm from '../lib/Confirm.svelte';
  import FrameworkIcon from '../lib/FrameworkIcon.svelte';
  import Overview from './project/Overview.svelte';
  import Deployments from './project/Deployments.svelte';
  import Logs from './project/Logs.svelte';
  import Environment from './project/Environment.svelte';
  import Domains from './project/Domains.svelte';
  import ProjectSettings from './project/ProjectSettings.svelte';

  let { id, tab, revision } = $props();

  let project = $state(null);
  let missing = $state(false);
  let deploying = $state(false);
  let confirmDelete = $state(false);

  async function load() {
    try {
      project = await api.projects.get(id);
      missing = false;
    } catch (err) {
      if (err.status === 404) {
        missing = true;
      } else {
        notify.error(err);
      }
    }
  }

  $effect(() => {
    id;
    revision;
    load();
  });

  const tabs = [
    { key: 'overview', label: 'Overview' },
    { key: 'deployments', label: 'Deployments' },
    { key: 'logs', label: 'Logs' },
    { key: 'env', label: 'Environment' },
    { key: 'domains', label: 'Domains' },
    { key: 'settings', label: 'Settings' },
  ];

  async function deploy() {
    deploying = true;
    try {
      const dep = await api.projects.deploy(project.id, {});
      notify.success('Deployment queued');
      navigate(`/projects/${project.slug}/logs?deployment=${dep.id}`);
      load();
    } catch (err) {
      notify.error(err);
    } finally {
      deploying = false;
    }
  }

  async function remove() {
    try {
      const result = await api.projects.remove(project.id);
      for (const warning of result.warnings ?? []) notify.error(warning);
      notify.success(`Deleted ${project.name}`);
      navigate('/');
    } catch (err) {
      notify.error(err);
    }
  }
</script>

{#if missing}
  <div class="gone">
    <h1>Project not found</h1>
    <p class="muted">There is no project called <code>{id}</code> on this server.</p>
    <a href="#/"><Button>Back to projects</Button></a>
  </div>
{:else if project === null}
  <div class="ghost"></div>
{:else}
  <header class="head">
    <div class="grow">
      <div class="row wrap">
        <FrameworkIcon framework={project.framework} title={project.frameworkName} size={24} />
        <h1>{project.name}</h1>
        {#if project.building}
          <Badge tone="busy" dot>Building</Badge>
        {:else if project.live}
          <Badge tone={statusTone(project.live.status)} dot>{statusLabel(project.live.status)}</Badge>
        {:else}
          <Badge tone="muted">Not deployed</Badge>
        {/if}
        {#if project.paused}<Badge tone="warn">Paused</Badge>{/if}
      </div>

      <div class="sub small">
        {#if project.url}
          <a href={project.url} target="_blank" rel="noreferrer noopener">
            {project.url.replace(/^https?:\/\//, '')} ↗
          </a>
        {:else}
          <span class="faint">No domain yet</span>
        {/if}
        {#if project.frameworkName}
          <span class="sep">·</span>
          <span class="muted nowrap">{project.frameworkName}</span>
        {/if}
        {#if project.repo}
          <span class="sep">·</span>
          <a
            href="https://github.com/{project.repo.owner}/{project.repo.name}"
            target="_blank"
            rel="noreferrer noopener"
          >
            {project.repo.owner}/{project.repo.name}
          </a>
          <span class="sep">·</span>
          <span class="muted">{project.repo.branch}</span>
        {/if}
        {#if subject(project.live?.message)}
          <span class="sep">·</span>
          <span class="muted truncate" title={project.live.message}>
            {subject(project.live.message)}
          </span>
        {/if}
        {#if project.live}
          <span class="sep">·</span>
          <span class="faint nowrap">{age(project.live.finishedAt ?? project.live.queuedAt)}</span>
        {/if}
      </div>
    </div>

    <div class="row">
      {#if project.building}
        <Button onclick={() => api.projects.cancel(project.id).then(load).catch(notify.error)}>
          Cancel
        </Button>
      {/if}
      <Button variant="primary" pending={deploying || project.building} onclick={deploy}>
        Deploy
      </Button>
    </div>
  </header>

  <nav class="tabs">
    {#each tabs as t}
      <a href="#/projects/{project.slug}/{t.key}" class:active={tab === t.key}>{t.label}</a>
    {/each}
  </nav>

  {#if tab === 'deployments'}
    <Deployments {project} onchange={load} />
  {:else if tab === 'logs'}
    <Logs {project} />
  {:else if tab === 'env'}
    <Environment {project} onchange={load} />
  {:else if tab === 'domains'}
    <Domains {project} onchange={load} />
  {:else if tab === 'settings'}
    <ProjectSettings {project} onchange={load} ondelete={() => (confirmDelete = true)} />
  {:else}
    <Overview {project} {revision} onchange={load} />
  {/if}
{/if}

{#if confirmDelete && project}
  <Confirm
    title="Delete {project.name}?"
    message="Its containers and images will be removed, and its GitHub webhook deleted. Data on shared volumes is kept."
    confirmLabel="Delete project"
    confirmText={project.name}
    danger
    onconfirm={remove}
    onclose={() => (confirmDelete = false)}
  />
{/if}

<style>
  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 14px;
  }

  .sub {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-top: 4px;
    flex-wrap: wrap;
  }
  .sep { color: var(--text-faint); }

  .tabs {
    display: flex;
    gap: 2px;
    margin-bottom: 20px;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
  }

  .tabs a {
    padding: 7px 12px;
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
    white-space: nowrap;
  }
  .tabs a:hover { color: var(--text); text-decoration: none; }
  .tabs a.active { color: var(--text); border-bottom-color: var(--accent); }

  .gone { padding: 60px 0; text-align: center; }
  .gone p { margin: 8px 0 16px; }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
  }

  .ghost {
    height: 200px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    animation: fade 1.4s ease-in-out infinite;
  }
  @keyframes fade { 50% { opacity: 0.55; } }
</style>
