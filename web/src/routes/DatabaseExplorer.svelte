<script>
  import { api } from '../lib/api.js';
  import { notify } from '../lib/toast.js';
  import Button from '../lib/Button.svelte';
  import Badge from '../lib/Badge.svelte';
  import Empty from '../lib/Empty.svelte';
  import DbIcon from '../lib/DbIcon.svelte';
  import DataGrid from '../lib/DataGrid.svelte';
  import { bytes } from '../lib/format.js';
  import { t } from '../lib/i18n.svelte.js';

  /**
   * Looking inside one database: what it holds on the left, a page of the
   * selected table, collection or key on the right, and — for SQL engines —
   * a read-only query box. Nothing here can change data; the server runs
   * every read in a read-only session.
   */
  let { slug, name } = $props();

  const PAGE = 50;

  let db = $state(null);
  let loadError = $state(null);
  let overview = $state(null);
  let overviewError = $state(null);
  let loadingOverview = $state(false);

  let search = $state('');
  let match = $state('');
  let selected = $state(null);
  let offset = $state(0);
  let filter = $state('');
  let page = $state(null);
  let pageError = $state(null);
  let loadingPage = $state(false);

  let mode = $state('browse');
  let sql = $state('');
  let queryResult = $state(null);
  let queryError = $state(null);
  let running = $state(false);

  const isRedis = $derived(db?.engine === 'redis');
  const isMongo = $derived(db?.engine === 'mongo');
  const objects = $derived(
    (overview?.objects ?? []).filter((o) => !search.trim() || o.name.toLowerCase().includes(search.trim().toLowerCase())),
  );

  async function loadDb() {
    try {
      const list = await api.databases.list(slug);
      db = list.find((d) => d.name === name) ?? null;
      if (!db) loadError = t('explore.notFound', { name, project: slug });
    } catch (err) {
      loadError = err.message;
    }
  }

  async function loadOverview() {
    loadingOverview = true;
    overviewError = null;
    try {
      overview = await api.databases.explore(slug, name, { match: isRedis ? match.trim() : '' });
      if (!selected && overview.objects.length) select(overview.objects[0]);
      if (!sql && overview.queryable && overview.objects.length) {
        const first = overview.objects[0].name;
        sql = `SELECT * FROM ${quote(first)} LIMIT 20`;
      }
    } catch (err) {
      overviewError = err.message;
    } finally {
      loadingOverview = false;
    }
  }

  function quote(n) {
    if (db?.engine === 'mysql') return '`' + n.replaceAll('`', '``') + '`';
    if (db?.engine === 'postgres' && n.includes('.')) {
      const [schema, rel] = [n.slice(0, n.indexOf('.')), n.slice(n.indexOf('.') + 1)];
      return `"${schema.replaceAll('"', '""')}"."${rel.replaceAll('"', '""')}"`;
    }
    return /^[a-z_][a-z0-9_]*$/.test(n) ? n : `"${n.replaceAll('"', '""')}"`;
  }

  async function loadPage() {
    if (!selected) return;
    loadingPage = true;
    pageError = null;
    try {
      page = await api.databases.explore(slug, name, {
        object: selected.name,
        offset,
        limit: PAGE,
        filter: isMongo ? filter.trim() : '',
      });
    } catch (err) {
      page = null;
      pageError = err.message;
    } finally {
      loadingPage = false;
    }
  }

  function select(o) {
    selected = o;
    offset = 0;
    filter = '';
    mode = 'browse';
    loadPage();
  }

  function go(delta) {
    offset = Math.max(0, offset + delta);
    loadPage();
  }

  async function runQuery() {
    if (!sql.trim()) return;
    running = true;
    queryError = null;
    try {
      queryResult = await api.databases.query(slug, name, sql, 200);
    } catch (err) {
      queryResult = null;
      queryError = err.message;
    } finally {
      running = false;
    }
  }

  function onSqlKey(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      runQuery();
    }
  }

  function count(o) {
    if (o.count === undefined || o.count === null) return '';
    const n = o.count.toLocaleString();
    return o.estimated ? `~${n}` : n;
  }

  function ttl(o) {
    if (o.ttl === undefined || o.ttl === null || o.ttl < 0) return '';
    if (o.ttl < 120) return `${o.ttl}s`;
    if (o.ttl < 7200) return `${Math.round(o.ttl / 60)}m`;
    if (o.ttl < 172800) return `${Math.round(o.ttl / 3600)}h`;
    return `${Math.round(o.ttl / 86400)}d`;
  }

  $effect(() => {
    slug;
    name;
    (async () => {
      await loadDb();
      if (db) await loadOverview();
    })();
  });
</script>

<nav class="crumbs small">
  <a href="#/databases">{t('nav.databases')}</a>
  <span>/</span>
  <a href="#/projects/{slug}/databases">{slug}</a>
  <span>/</span>
  <span class="here">{name}</span>
</nav>

{#if loadError}
  <Empty title={t('explore.cannotOpen')} description={loadError} />
{:else if !db}
  <p class="muted small">{t('common.loading')}</p>
{:else}
  <header class="top">
    <div class="who">
      <DbIcon engine={db.engine} size={26} />
      <div>
        <h1>{db.name}</h1>
        <div class="meta">
          <Badge>{t(`db.engine.${db.engine}`)}{db.version ? ` ${db.version}` : ''}</Badge>
          <Badge tone={db.status.state === 'running' || db.status.state === 'ready' ? 'good' : 'bad'} dot>
            {t(`db.state.${db.status.state}`)}
          </Badge>
          {#if overview?.size}<span class="small muted">{bytes(overview.size)}</span>{/if}
          {#if overview?.keys !== undefined && overview?.keys !== null}
            <span class="small muted">{t('explore.keys', { count: overview.keys })}</span>
          {/if}
          <span class="small muted">{t('explore.readOnly')}</span>
        </div>
      </div>
    </div>
    <Button size="sm" pending={loadingOverview} onclick={loadOverview}>{t('common.refresh')}</Button>
  </header>

  {#if db.status.state !== 'running' && db.status.state !== 'ready'}
    <p class="warn small">{db.status.detail || t('explore.notRunning')}</p>
  {/if}

  <div class="layout">
    <aside class="objects">
      <div class="search">
        {#if isRedis}
          <input
            class="text-input"
            bind:value={match}
            placeholder={t('explore.matchPlaceholder')}
            spellcheck="false"
            onkeydown={(e) => e.key === 'Enter' && loadOverview()}
          />
        {/if}
        <input class="text-input" bind:value={search} placeholder={t(`explore.filterObjects.${db.engine}`)} spellcheck="false" />
      </div>
      {#if overviewError}
        <p class="warn small pad">{overviewError}</p>
      {:else if overview === null}
        <p class="muted small pad">{t('common.loading')}</p>
      {:else if overview.objects.length === 0}
        <p class="muted small pad">{t(`explore.empty.${db.engine}`)}</p>
      {:else}
        <ul>
          {#each objects as o (o.name)}
            <li>
              <button type="button" class:active={selected?.name === o.name} onclick={() => select(o)} title={o.name}>
                <span class="oname mono">{o.name}</span>
                <span class="ometa">
                  {#if o.kind !== 'table' && o.kind !== 'collection'}<span class="kind">{o.kind}</span>{/if}
                  {#if ttl(o)}<span class="kind">⏱ {ttl(o)}</span>{/if}
                  <span>{count(o)}</span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
        {#if overview.truncated}<p class="muted small pad">{t('explore.truncated')}</p>{/if}
      {/if}
    </aside>

    <section class="main">
      {#if overview?.queryable}
        <div class="modes">
          <button type="button" class:active={mode === 'browse'} onclick={() => (mode = 'browse')}>{t('explore.browse')}</button>
          <button type="button" class:active={mode === 'query'} onclick={() => (mode = 'query')}>{t('explore.query')}</button>
        </div>
      {/if}

      {#if mode === 'query'}
        <textarea
          class="sql mono text-input"
          rows="5"
          bind:value={sql}
          onkeydown={onSqlKey}
          spellcheck="false"
          placeholder="SELECT * FROM …"
        ></textarea>
        <div class="bar">
          <span class="small muted">{t('explore.queryHint')}</span>
          <Button size="sm" variant="primary" pending={running} disabled={!sql.trim()} onclick={runQuery}>
            {t('explore.run')}
          </Button>
        </div>
        {#if queryError}
          <pre class="error small">{queryError}</pre>
        {:else if queryResult}
          <p class="small muted">
            {t('explore.rowsBack', { count: queryResult.rows.length })}{queryResult.more ? ` · ${t('explore.moreCut')}` : ''}
            · {queryResult.elapsedMs} ms
          </p>
          {#if queryResult.columns.length}
            <DataGrid columns={queryResult.columns} rows={queryResult.rows} />
          {/if}
        {/if}
      {:else if !selected}
        <p class="muted small">{t('explore.pick')}</p>
      {:else}
        <div class="bar">
          <div class="obj">
            <strong class="mono">{selected.name}</strong>
            {#if page?.kind && isRedis}<Badge tone="muted">{page.kind}</Badge>{/if}
          </div>
          {#if isMongo}
            <input
              class="filter mono text-input"
              bind:value={filter}
              placeholder={'{"status": "active"}'}
              spellcheck="false"
              onkeydown={(e) => e.key === 'Enter' && ((offset = 0), loadPage())}
            />
          {/if}
        </div>

        {#if pageError}
          <pre class="error small">{pageError}</pre>
        {:else if page === null}
          <p class="muted small">{t('common.loading')}</p>
        {:else}
          {#if page.rows.length === 0}
            <p class="muted small">{t('explore.noRows')}</p>
            {#if page.columns.length}<p class="small faint mono">{page.columns.join(' · ')}</p>{/if}
          {:else}
            <DataGrid columns={page.columns} rows={page.rows} offset={page.offset} />
          {/if}
          <div class="pager">
            <span class="small muted">
              {#if page.rows.length}
                {t('explore.range', {
                  from: (page.offset + 1).toLocaleString(),
                  to: (page.offset + page.rows.length).toLocaleString(),
                })}{page.total !== undefined && page.total !== null ? ` / ${page.total.toLocaleString()}` : ''}
              {/if}
              {#if page.truncated}· {t('explore.cellsCut')}{/if}
              · {page.elapsedMs} ms
            </span>
            <span class="pages">
              <Button size="sm" disabled={offset === 0 || loadingPage} onclick={() => go(-PAGE)}>{t('explore.prev')}</Button>
              <Button size="sm" disabled={!page.more || loadingPage} pending={loadingPage} onclick={() => go(PAGE)}>
                {t('explore.next')}
              </Button>
            </span>
          </div>
        {/if}
      {/if}
    </section>
  </div>
{/if}

<style>
  .crumbs { display: flex; gap: 6px; color: var(--text-muted); margin-bottom: 12px; flex-wrap: wrap; }
  .crumbs .here { color: var(--text); }
  .top { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 14px; }
  .who { display: flex; gap: 12px; align-items: center; min-width: 0; }
  h1 { margin: 0 0 4px; font-size: 20px; overflow-wrap: anywhere; }
  .meta { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .warn { color: var(--warn); }
  .pad { padding: 10px 12px; margin: 0; }

  .layout { display: grid; grid-template-columns: 250px minmax(0, 1fr); gap: 16px; align-items: start; }
  .objects {
    border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg-raised);
    max-height: 74vh; display: flex; flex-direction: column; overflow: hidden;
    position: sticky; top: 68px;
  }
  .search { display: flex; flex-direction: column; gap: 6px; padding: 8px; border-bottom: 1px solid var(--border); }
  .search input { width: 100%; font-size: 12.5px; }
  ul { list-style: none; margin: 0; padding: 4px; overflow-y: auto; }
  li button {
    all: unset; box-sizing: border-box; width: 100%; cursor: pointer;
    display: flex; justify-content: space-between; gap: 8px; align-items: baseline;
    padding: 5px 8px; border-radius: var(--radius-sm); font-size: 12.5px;
  }
  li button:hover { background: var(--bg-hover); }
  li button.active { background: var(--accent-bg); color: var(--text); }
  li button:focus-visible { outline: 2px solid var(--accent); }
  .oname { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .ometa { display: flex; gap: 6px; color: var(--text-muted); font-size: 11.5px; white-space: nowrap; font-variant-numeric: tabular-nums; }
  .kind { font-size: 10.5px; padding: 0 5px; border-radius: 3px; background: var(--bg-sunken); }

  .main { min-width: 0; display: flex; flex-direction: column; gap: 10px; }
  .modes { display: flex; gap: 2px; border-bottom: 1px solid var(--border); }
  .modes button {
    all: unset; cursor: pointer; padding: 6px 12px; font-size: 13px; font-weight: 500;
    color: var(--text-muted); border-bottom: 2px solid transparent; margin-bottom: -1px;
  }
  .modes button.active { color: var(--text); border-bottom-color: var(--accent); }
  .bar { display: flex; justify-content: space-between; align-items: center; gap: 10px; flex-wrap: wrap; }
  .obj { display: flex; gap: 8px; align-items: center; min-width: 0; overflow-wrap: anywhere; }
  .filter { flex: 1; min-width: 200px; max-width: 420px; font-size: 12.5px; }
  .sql { width: 100%; font-size: 13px; line-height: 1.5; resize: vertical; }
  .pager { display: flex; justify-content: space-between; align-items: center; gap: 10px; flex-wrap: wrap; }
  .pages { display: flex; gap: 6px; }
  .error {
    margin: 0; padding: 9px 11px; background: var(--bad-bg); color: var(--bad);
    border-left: 3px solid var(--bad); border-radius: var(--radius-sm);
    white-space: pre-wrap; overflow-wrap: anywhere; font-family: var(--mono);
  }

  @media (max-width: 760px) {
    .layout { grid-template-columns: 1fr; }
    .objects { position: static; max-height: 40vh; }
  }
</style>
