<script>
  import { api } from '../../lib/api.js';
  import { age, exact, duration, subject, statusTone, statusLabel } from '../../lib/format.js';
  import { notify } from '../../lib/toast.js';
  import { navigate } from '../../lib/router.js';
  import Badge from '../../lib/Badge.svelte';
  import Button from '../../lib/Button.svelte';
  import Empty from '../../lib/Empty.svelte';
  import Confirm from '../../lib/Confirm.svelte';

  let { project, onchange } = $props();

  let rollbackTarget = $state(null);
  let plan = $state(null);

  async function openRollback(deployment) {
    rollbackTarget = deployment;
    plan = null;
    try {
      plan = await api.projects.rollbackPlan(project.id, deployment.id);
    } catch (err) {
      notify.error(err);
    }
  }

  async function rollback() {
    if (plan && !plan.possible) {
      notify.error(plan.reason);
      return;
    }
    try {
      const dep = await api.projects.rollback(project.id, rollbackTarget.id);
      notify.success('Rolling back');
      rollbackTarget = null;
      navigate(`/projects/${project.slug}/logs?deployment=${dep.id}`);
      onchange();
    } catch (err) {
      notify.error(err);
    }
  }

  const deployments = $derived(project.deployments ?? []);
</script>

{#if deployments.length === 0}
  <div class="panel">
    <Empty title="No deployments yet" description="Deploy this project to see its history here." />
  </div>
{:else}
  <ul class="list">
    {#each deployments as d (d.id)}
      <li class="item" class:current={d.id === project.current}>
        <div class="left grow">
          <div class="row wrap">
            <Badge tone={statusTone(d.status)} dot>{statusLabel(d.status)}</Badge>
            {#if d.id === project.current}<Badge tone="good">Current</Badge>{/if}
            {#if d.rolledBackFrom}<Badge tone="warn">Rollback</Badge>{/if}
            <span class="num faint">#{d.number}</span>
          </div>

          <div class="msg truncate" title={d.message}>
            {#if d.short}<code>{d.short}</code>{/if}
            {subject(d.message) || 'No commit message'}
          </div>

          <div class="meta small faint">
            <span>{d.trigger}</span>
            {#if d.author}<span>·</span><span>{d.author}</span>{/if}
            {#if d.branch}<span>·</span><span>{d.branch}</span>{/if}
            <span>·</span>
            <span title={exact(d.queuedAt)}>{age(d.finishedAt ?? d.queuedAt)}</span>
            {#if d.startedAt}
              <span>·</span><span>{duration(d.startedAt, d.finishedAt)}</span>
            {/if}
          </div>

          {#if d.error}
            <p class="err small">{d.error.split('\n')[0]}</p>
          {/if}
        </div>

        <div class="right row">
          <a href="#/projects/{project.slug}/logs?deployment={d.id}">
            <Button size="sm" variant="ghost">Logs</Button>
          </a>
          {#if d.id !== project.current && d.status !== 'failed'}
            <Button size="sm" onclick={() => openRollback(d)}>Roll back</Button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>

  <p class="muted small foot">
    Only the live deployment keeps containers running. Rolling back to an earlier commit rebuilds
    it — instantly when its image is still on disk, which publix keeps for the two most recent
    deployments.
  </p>
{/if}

{#if rollbackTarget}
  <Confirm
    title="Roll back to #{rollbackTarget.number}?"
    message={rollbackTarget.short
      ? `This brings back commit ${rollbackTarget.short} and moves production traffic to it once it is healthy.`
      : 'This brings that deployment back and moves production traffic to it once it is healthy.'}
    confirmLabel="Roll back"
    onconfirm={rollback}
    onclose={() => (rollbackTarget = null)}
  >
    {#snippet children()}
      {#if plan === null}
        <p class="small muted">Checking what this will involve…</p>
      {:else if plan.instant}
        <p class="small ok">⚡ The image for this commit is still on disk — this will be fast.</p>
      {:else if plan.rebuild}
        <p class="small muted">
          This commit's image has been pruned, so it will be rebuilt from source. The result is
          the same; it just takes as long as a normal build.
        </p>
      {:else if !plan.possible}
        <p class="small err">{plan.reason}</p>
      {/if}
    {/snippet}
  </Confirm>
{/if}

<style>
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .item {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .item:last-child { border-bottom: none; }
  .item:hover { background: var(--bg-hover); }
  .current { background: var(--accent-bg); }
  .current:hover { background: var(--accent-bg); }

  .left { display: flex; flex-direction: column; gap: 4px; min-width: 0; }

  .msg { display: flex; align-items: center; gap: 7px; font-size: 13px; }
  .meta { display: flex; gap: 6px; flex-wrap: wrap; }
  .num { font-variant-numeric: tabular-nums; }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }
  .current code { background: var(--bg-raised); }

  .err { margin: 2px 0 0; color: var(--bad); }
  .ok { color: var(--good); }

  .right { flex: none; }

  .foot { margin-top: 12px; max-width: 76ch; line-height: 1.55; }

  .panel {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
</style>
