<script>
  import { api } from '../lib/api.js';
  import { age, statusTone, statusLabel, subject, isActive } from '../lib/format.js';
  import { notify } from '../lib/toast.js';
  import { t } from '../lib/i18n.svelte.js';
  import Badge from '../lib/Badge.svelte';
  import Button from '../lib/Button.svelte';
  import Empty from '../lib/Empty.svelte';
  import FrameworkIcon from '../lib/FrameworkIcon.svelte';

  let { revision } = $props();

  let projects = $state(null);
  let deploying = $state({});

  async function load() {
    try {
      projects = await api.projects.list();
    } catch (err) {
      notify.error(err);
      projects = [];
    }
  }

  $effect(() => {
    revision;
    load();
  });

  async function deploy(project, event) {
    event.preventDefault();
    event.stopPropagation();
    deploying = { ...deploying, [project.id]: true };
    try {
      await api.projects.deploy(project.id, {});
      notify.success(t('projects.deployingToast', { name: project.name }));
      load();
    } catch (err) {
      notify.error(err);
    } finally {
      deploying = { ...deploying, [project.id]: false };
    }
  }
</script>

<div class="spread head">
  <div>
    <h1>{t('projects.title')}</h1>
    <p class="muted small">
      {#if projects?.length}
        {t('projects.count', { count: projects.length })}
      {:else}
        {t('projects.subtitle')}
      {/if}
    </p>
  </div>
  <a href="#/import"><Button variant="primary">{t('projects.importCta')}</Button></a>
</div>

{#if projects === null}
  <div class="skeleton">
    {#each Array(3) as _}<div class="ghost"></div>{/each}
  </div>
{:else if projects.length === 0}
  <div class="panel">
    <Empty
      title={t('projects.emptyTitle')}
      description={t('projects.emptyDesc')}
    >
      <a href="#/import"><Button variant="primary">{t('projects.importCta')}</Button></a>
    </Empty>
  </div>
{:else}
  <div class="grid">
    {#each projects as project (project.id)}
      <a class="card" href="#/projects/{project.slug}">
        <div class="spread">
          <div class="row grow" style="min-width:0">
            <FrameworkIcon framework={project.framework} title={project.frameworkName} />
            <h2 class="truncate">{project.name}</h2>
          </div>
          {#if project.building}
            <Badge tone="busy" dot>{t('status.building')}</Badge>
          {:else if project.live}
            <Badge tone={statusTone(project.live.status)} dot>
              {statusLabel(project.live.status)}
            </Badge>
          {:else if project.paused}
            <Badge tone="warn">{t('status.paused')}</Badge>
          {:else if project.latest?.status === 'failed'}
            <Badge tone="bad" dot>{t('status.failed')}</Badge>
          {:else}
            <Badge tone="muted">{t('status.notDeployed')}</Badge>
          {/if}
        </div>

        {#if project.url}
          <span class="url truncate">{project.url.replace(/^https?:\/\//, '')}</span>
        {:else}
          <span class="url faint">{t('projects.noDomain')}</span>
        {/if}

        <div class="meta">
          {#if project.repo}
            <span class="truncate" title="{project.repo.owner}/{project.repo.name}">
              {project.repo.owner}/{project.repo.name}
            </span>
            <span class="sep">·</span>
            <span class="nowrap">{project.repo.branch}</span>
          {:else}
            <span class="faint">{t('projects.noRepo')}</span>
          {/if}
          {#if project.frameworkName}
            <span class="sep">·</span><span class="nowrap">{project.frameworkName}</span>
          {/if}
        </div>

        <div class="foot">
          {#if project.live || project.latest}
            {@const d = project.live ?? project.latest}
            <span class="commit" title={d.error || d.message}>
              {#if d.short}<code>{d.short}</code>{/if}
              <span class="truncate" class:err={!project.live && d.status === 'failed'}>
                {#if !project.live && d.status === 'failed'}
                  {d.error?.split('\n')[0] || t('projects.deployFailed')}
                {:else}
                  {subject(d.message) || t('projects.noCommitMessage')}
                {/if}
              </span>
            </span>
            <span class="faint nowrap">{age(d.finishedAt ?? d.queuedAt)}</span>
          {:else}
            <span class="faint">{t('projects.neverDeployed')}</span>
          {/if}
        </div>

        <div class="actions">
          <Button
            size="sm"
            pending={deploying[project.id] || project.building}
            disabled={!project.repo && !project.live}
            onclick={(e) => deploy(project, e)}
          >
            {project.building ? t('status.deploying') : t('projects.deploy')}
          </Button>
        </div>
      </a>
    {/each}
  </div>
{/if}

<style>
  .head { margin-bottom: 18px; }
  .head p { margin: 2px 0 0; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 12px;
  }

  .card {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding: 14px 16px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    transition: border-color 0.12s, box-shadow 0.12s;
  }
  .card:hover {
    text-decoration: none;
    border-color: var(--border-strong);
    box-shadow: var(--shadow);
  }

  h2 { font-size: 14.5px; }

  .url {
    font-size: 12.5px;
    color: var(--accent-text);
    font-family: var(--mono);
  }
  .url.faint { color: var(--text-faint); font-family: var(--font); }

  .meta,
  .foot {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-muted);
    min-width: 0;
  }

  .foot {
    padding-top: 8px;
    border-top: 1px solid var(--border);
    justify-content: space-between;
  }

  .commit { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .commit .truncate { min-width: 0; }
  .err { color: var(--bad); }

  .sep { color: var(--text-faint); }

  code {
    padding: 0 4px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-size: 11px;
  }

  /* The deploy button sits above the card link so clicking it does not
     also navigate into the project. */
  .actions {
    position: absolute;
    right: 14px;
    bottom: 12px;
    opacity: 0;
    transition: opacity 0.12s;
  }
  .card:hover .actions,
  .actions:focus-within { opacity: 1; }
  .card:hover .foot .faint { visibility: hidden; }

  .skeleton { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 12px; }
  .ghost {
    height: 132px;
    border-radius: var(--radius);
    background: var(--bg-raised);
    border: 1px solid var(--border);
    animation: fade 1.4s ease-in-out infinite;
  }
  @keyframes fade { 50% { opacity: 0.55; } }

  .panel {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
</style>
