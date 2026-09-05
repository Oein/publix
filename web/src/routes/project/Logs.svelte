<script>
  import { api, buildLogStream, runtimeLogStream } from '../../lib/api.js';
  import { route } from '../../lib/router.js';
  import { isActive, statusTone, statusLabel, subject, age } from '../../lib/format.js';
  import Badge from '../../lib/Badge.svelte';
  import Button from '../../lib/Button.svelte';
  import LogView from '../../lib/LogView.svelte';
  import { t } from '../../lib/i18n.svelte.js';

  let { project } = $props();

  let mode = $state('build');
  let selected = $state('');
  let lines = $state([]);
  let streaming = $state(false);

  // A ?deployment= in the URL means someone followed a "Logs" link for a
  // specific build, so honour it rather than defaulting to the newest.
  route.subscribe((r) => {
    const wanted = r.query.get('deployment');
    if (wanted) selected = wanted;
  });

  const deployments = $derived(project.deployments ?? []);

  $effect(() => {
    if (!selected && deployments.length > 0) {
      selected = deployments[0].id;
    }
  });

  const deployment = $derived(deployments.find((d) => d.id === selected) ?? null);

  // Build logs: replay the whole log, then follow. The server sends history
  // first on the stream, so there is nothing to stitch together here.
  $effect(() => {
    if (mode !== 'build' || !selected) return;
    lines = [];
    streaming = true;

    const stop = buildLogStream(project.id, selected, {
      line: (line) => (lines = [...lines, line]),
      done: () => (streaming = false),
      error: () => (streaming = false),
    });
    return () => {
      stop();
      streaming = false;
    };
  });

  // Runtime logs come from the containers and are always a live tail.
  $effect(() => {
    if (mode !== 'runtime') return;
    lines = [];
    streaming = true;

    let seq = 0;
    const stop = runtimeLogStream(project.id, {
      line: (line) => {
        seq += 1;
        lines = [...lines, { seq, text: line.text, container: line.container }];
        // A live tail runs indefinitely; cap what is held in memory so a
        // chatty service cannot grow the tab without bound.
        if (lines.length > 4000) lines = lines.slice(-3000);
      },
      error: () => (streaming = false),
    });
    return () => {
      stop();
      streaming = false;
    };
  });
</script>

<div class="bar">
  <div class="switch">
    <button class:active={mode === 'build'} onclick={() => (mode = 'build')}>
      {t('logs.build')}
    </button>
    <button class:active={mode === 'runtime'} onclick={() => (mode = 'runtime')}>
      {t('logs.runtime')}
    </button>
  </div>

  {#if mode === 'build'}
    {#if deployments.length > 0}
      <select bind:value={selected}>
        {#each deployments as d}
          <option value={d.id}>
            #{d.number} · {d.short || d.id.slice(0, 8)} · {subject(d.message) || d.trigger}
          </option>
        {/each}
      </select>
    {/if}
    {#if deployment}
      <Badge tone={statusTone(deployment.status)} dot>{statusLabel(deployment.status)}</Badge>
      <span class="small faint nowrap">{age(deployment.finishedAt ?? deployment.queuedAt)}</span>
    {/if}
  {:else}
    <span class="small muted">{t('logs.runtimeBlurb')}</span>
  {/if}

  <span class="grow"></span>

  {#if streaming}
    <span class="small muted row"><span class="pulse"></span> {t('logs.streaming')}</span>
  {/if}
</div>

{#if mode === 'build' && deployments.length === 0}
  <div class="panel empty">{t('logs.notDeployed')}</div>
{:else}
  <LogView
    {lines}
    {streaming}
    height="min(62vh, 620px)"
    empty={mode === 'build' ? t('logs.noBuildOutput') : t('logs.waiting')}
  />
{/if}

{#if mode === 'build' && deployment?.error}
  <div class="failure">
    <strong>{t('logs.failed')}</strong>
    <pre>{deployment.error}</pre>
  </div>
{/if}

<style>
  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
    flex-wrap: wrap;
  }

  .switch {
    display: inline-flex;
    padding: 2px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  .switch button {
    padding: 4px 12px;
    border: none;
    background: none;
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-muted);
    border-radius: 4px;
    cursor: pointer;
  }
  .switch button.active { background: var(--bg-raised); color: var(--text); box-shadow: var(--shadow); }

  select {
    max-width: 46ch;
    padding: 5px 9px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
  }

  .pulse {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--busy);
    animation: pulse 1.4s ease-in-out infinite;
  }
  @keyframes pulse { 50% { opacity: 0.3; } }

  .panel {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .empty { padding: 44px; text-align: center; color: var(--text-muted); }

  .failure {
    margin-top: 12px;
    padding: 12px 14px;
    background: var(--bad-bg);
    border-left: 3px solid var(--bad);
    border-radius: var(--radius-sm);
  }
  .failure pre {
    margin: 6px 0 0;
    font-family: var(--mono);
    font-size: 12.5px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
