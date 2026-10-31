<script>
  import DatabaseList from '../lib/DatabaseList.svelte';
  import DbIcon from '../lib/DbIcon.svelte';
  import { t } from '../lib/i18n.svelte.js';

  /**
   * Every database on the server in one place: what runs where, whether it
   * is up, when it was last backed up — and the way into each one's data.
   */
  let { revision } = $props();

  let databases = $state([]);
  let filter = $state('');
  let engine = $state('');

  const engines = ['postgres', 'mysql', 'mongo', 'redis', 'sqlite'];
  const counts = $derived(Object.fromEntries(engines.map((e) => [e, databases.filter((d) => d.engine === e).length])));
  const down = $derived(databases.filter((d) => !['running', 'ready'].includes(d.status?.state)).length);
  const unscheduled = $derived(databases.filter((d) => !d.backup?.schedule).length);
  const failed = $derived(databases.filter((d) => d.backups?.[0]?.status === 'failed').length);
</script>

<div class="head">
  <div>
    <h1>{t('dbs.title')}</h1>
    <p class="muted small">{t('dbs.subtitle')}</p>
  </div>
</div>

<div class="stats">
  <button type="button" class="stat" class:active={engine === ''} onclick={() => (engine = '')}>
    <span class="n">{databases.length}</span>
    <span class="l">{t('dbs.all')}</span>
  </button>
  {#each engines as e}
    {#if counts[e]}
      <button type="button" class="stat" class:active={engine === e} onclick={() => (engine = engine === e ? '' : e)}>
        <DbIcon engine={e} size={16} tile={false} />
        <span class="n">{counts[e]}</span>
        <span class="l">{t(`db.engine.${e}`)}</span>
      </button>
    {/if}
  {/each}
  <span class="health small">
    {#if down}<span class="bad">{t('dbs.down', { count: down })}</span>{/if}
    {#if failed}<span class="bad">{t('dbs.failedBackups', { count: failed })}</span>{/if}
    {#if unscheduled}
      <a href="#/settings/backups" class="warn">{t('dbs.unscheduled', { count: unscheduled })}</a>
    {/if}
    {#if databases.length && !down && !failed && !unscheduled}<span class="good">{t('dbs.allGood')}</span>{/if}
  </span>
</div>

<input class="search text-input" bind:value={filter} placeholder={t('dbs.search')} spellcheck="false" />

<DatabaseList {revision} {filter} engineFilter={engine} onloaded={(list) => (databases = list)} />

<style>
  .head { margin-bottom: 14px; }
  h1 { margin-bottom: 2px; }
  .stats { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; margin-bottom: 12px; }
  .stat {
    display: inline-flex; gap: 6px; align-items: center;
    padding: 6px 11px; border: 1px solid var(--border); border-radius: var(--radius-sm);
    background: var(--bg-raised); color: var(--text); cursor: pointer; font-size: 13px;
  }
  .stat:hover { background: var(--bg-hover); }
  .stat.active { border-color: var(--accent); background: var(--accent-bg); }
  .stat .n { font-weight: 650; font-variant-numeric: tabular-nums; }
  .stat .l { color: var(--text-muted); }
  .health { display: flex; gap: 12px; margin-left: auto; flex-wrap: wrap; }
  .bad { color: var(--bad); }
  .warn { color: var(--warn); }
  .good { color: var(--good); }
  .search { width: 100%; max-width: 360px; margin-bottom: 12px; }
</style>
