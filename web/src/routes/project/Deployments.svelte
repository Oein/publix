<script>
  import { api } from '../../lib/api.js';
  import { age, exact, duration, subject, statusTone, statusLabel } from '../../lib/format.js';
  import { notify } from '../../lib/toast.js';
  import { navigate } from '../../lib/router.js';
  import { t } from '../../lib/i18n.svelte.js';
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
      notify.success(t('deployments.rollingBack'));
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
    <Empty title={t('deployments.emptyTitle')} description={t('deployments.emptyDesc')} />
  </div>
{:else}
  <ul class="list">
    {#each deployments as d (d.id)}
      <li class="item" class:current={d.id === project.current}>
        <div class="left grow">
          <div class="row wrap">
            <Badge tone={statusTone(d.status)} dot>{statusLabel(d.status)}</Badge>
            {#if d.id === project.current}<Badge tone="good">{t('status.current')}</Badge>{/if}
            {#if d.rolledBackFrom}<Badge tone="warn">{t('status.rollback')}</Badge>{/if}
            <span class="num faint">#{d.number}</span>
          </div>

          <div class="msg truncate" title={d.message}>
            {#if d.short}<code>{d.short}</code>{/if}
            {subject(d.message) || t('projects.noCommitMessage')}
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
            <Button size="sm" variant="ghost">{t('deployments.logs')}</Button>
          </a>
          {#if d.id !== project.current && d.status !== 'failed'}
            <Button size="sm" onclick={() => openRollback(d)}>{t('deployments.rollback')}</Button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>

  <p class="muted small foot">{t('deployments.note')}</p>
{/if}

{#if rollbackTarget}
  <Confirm
    title={t('deployments.rollbackTitle', { number: rollbackTarget.number })}
    message={rollbackTarget.short
      ? t('deployments.rollbackMessageCommit', { short: rollbackTarget.short })
      : t('deployments.rollbackMessage')}
    confirmLabel={t('deployments.rollback')}
    onconfirm={rollback}
    onclose={() => (rollbackTarget = null)}
  >
    {#snippet children()}
      {#if plan === null}
        <p class="small muted">{t('deployments.checking')}</p>
      {:else if plan.instant}
        <p class="small ok">{t('deployments.instant')}</p>
      {:else if plan.rebuild}
        <p class="small muted">{t('deployments.rebuild')}</p>
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
