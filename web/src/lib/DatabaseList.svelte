<script>
  import { api } from './api.js';
  import { navigate } from './router.js';
  import { notify } from './toast.js';
  import Button from './Button.svelte';
  import Card from './Card.svelte';
  import Field from './Field.svelte';
  import Badge from './Badge.svelte';
  import Empty from './Empty.svelte';
  import Modal from './Modal.svelte';
  import Confirm from './Confirm.svelte';
  import DbIcon from './DbIcon.svelte';
  import LogView from './LogView.svelte';
  import { age, bytes, exact } from './format.js';
  import { t } from './i18n.svelte.js';

  /**
   * Databases and everything done to them: one project's (its Databases
   * tab), or every project's (the Databases page) when no project is given.
   *
   * What matters most here is the list of variables each database hands
   * the app, because that is what someone wiring up an app needs to read;
   * the container behind it is publix's business.
   */
  let { project = null, revision = 0, filter = '', engineFilter = '', onchange, onloaded } = $props();

  const all = $derived(project === null);
  // In the server-wide list a database's project travels with it.
  const slugOf = (db) => db.project?.slug ?? project.slug;
  const keyOf = (db) => `${slugOf(db)}/${db.name}`;

  let projects = $state([]);
  let addProject = $state('');
  let logsOf = $state(null);
  let logLines = $state(null);

  const shown = $derived(
    (databases ?? []).filter((db) => {
      if (engineFilter && db.engine !== engineFilter) return false;
      const q = filter.trim().toLowerCase();
      if (!q) return true;
      return [db.name, db.engine, db.project?.slug, db.project?.name].some((v) => v?.toLowerCase().includes(q));
    }),
  );

  let databases = $state(null);
  let targets = $state([]);

  let adding = $state(false);
  let engine = $state('postgres');
  let name = $state('');
  let version = $state('');
  let envPrefix = $state('');
  let saving = $state(false);
  let addError = $state(null);

  let scheduling = $state(null);
  let schedule = $state('@daily');
  let target = $state('');
  let keep = $state(7);

  let removing = $state(null);
  let deleteData = $state(false);

  let busy = $state({});
  let backupsOf = $state(null);
  let backups = $state(null);
  let restoring = $state(null);

  const engines = ['postgres', 'mysql', 'mongo', 'redis', 'sqlite'];

  async function load() {
    try {
      [databases, targets] = await Promise.all([
        all ? api.databases.all() : api.databases.list(project.slug),
        api.backupTargets.list(),
      ]);
      onloaded?.(databases);
    } catch (err) {
      notify.error(err);
    }
  }

  $effect(() => {
    project?.updatedAt;
    revision;
    load();
  });

  function stateTone(state) {
    if (state === 'running' || state === 'ready') return 'good';
    if (state === 'restarting') return 'warn';
    if (state === 'missing' || state === 'stopped') return 'bad';
    return 'muted';
  }

  async function openAdd() {
    if (all) {
      try {
        projects = await api.projects.list();
      } catch (err) {
        notify.error(err);
        return;
      }
      addProject = projects[0]?.slug ?? '';
    }
    engine = 'postgres';
    name = '';
    version = '';
    envPrefix = '';
    addError = null;
    adding = true;
  }

  async function add() {
    saving = true;
    addError = null;
    try {
      const res = await api.databases.create(all ? addProject : project.slug, {
        engine,
        name: name.trim(),
        version: version.trim(),
        envPrefix: envPrefix.trim(),
      });
      notify.success(t('db.created', { name: res.database.name }));
      adding = false;
      await load();
      onchange?.();
    } catch (err) {
      addError = { message: err.message, details: err.details ?? [] };
    } finally {
      saving = false;
    }
  }

  function openSchedule(db) {
    schedule = db.backup?.schedule ?? '@daily';
    target = db.backup?.target ?? targets[0]?.name ?? '';
    keep = db.backup?.keep ?? 7;
    scheduling = db;
  }

  async function saveSchedule(off = false) {
    try {
      await api.databases.update(slugOf(scheduling), scheduling.name, {
        backup: off ? { schedule: '' } : { schedule: schedule.trim(), target, keep: Number(keep) },
      });
      notify.success(off ? t('db.scheduleOff') : t('db.scheduleSaved'));
      scheduling = null;
      await load();
    } catch (err) {
      notify.error(err);
    }
  }

  async function act(db, kind, fn) {
    busy = { ...busy, [keyOf(db)]: kind };
    try {
      await fn();
      await load();
    } catch (err) {
      notify.error(err);
    } finally {
      busy = { ...busy, [keyOf(db)]: null };
    }
  }

  const restart = (db) =>
    act(db, 'restart', async () => {
      await api.databases.restart(slugOf(db), db.name);
      notify.success(t('db.restarted', { name: db.name }));
    });

  const backupNow = (db) =>
    act(db, 'backup', async () => {
      const rec = await api.databases.backup(slugOf(db), db.name);
      notify.success(t('db.backedUp', { file: rec.file }));
      if (backupsOf && keyOf(backupsOf) === keyOf(db)) await openBackups(db);
    });

  async function openBackups(db) {
    backupsOf = db;
    backups = null;
    try {
      backups = await api.databases.backups(slugOf(db), db.name);
    } catch (err) {
      backups = { error: err.message, files: [], history: db.backups ?? [] };
    }
  }

  async function restore() {
    const { db, file } = restoring;
    await act(db, 'restore', async () => {
      const res = await api.databases.restore(slugOf(db), db.name, { file });
      notify.success(res.note ?? t('db.restored', { file }));
    });
    restoring = null;
  }

  async function openLogs(db) {
    logsOf = db;
    logLines = null;
    try {
      logLines = (await api.databases.logs(slugOf(db), db.name, 300)).lines ?? [];
    } catch (err) {
      logLines = [err.message];
    }
  }

  async function remove() {
    try {
      await api.databases.remove(slugOf(removing), removing.name, deleteData);
      notify.success(t('db.deleted', { name: removing.name }));
      removing = null;
      await load();
      onchange?.();
    } catch (err) {
      notify.error(err);
    }
  }
</script>

<Card title={all ? t('dbs.listTitle') : t('db.title')} description={all ? t('dbs.listDesc') : t('db.desc')} flush>
  {#snippet actions()}
    <Button size="sm" variant="primary" onclick={openAdd}>{t('db.add')}</Button>
  {/snippet}

  {#if databases === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if databases.length === 0}
    <Empty title={t('db.noneTitle')} description={all ? t('dbs.noneDesc') : t('db.noneDesc')}>
      <Button variant="primary" onclick={openAdd}>{t('db.add')}</Button>
    </Empty>
  {:else if shown.length === 0}
    <p class="pad muted small">{t('dbs.noMatch')}</p>
  {:else}
    {#each shown as db (db.id)}
      <section class="db">
        <header>
          <div class="head">
            <DbIcon engine={db.engine} size={18} />
            <div class="title">
              <div class="line">
                <a class="name" href="#/databases/{slugOf(db)}/{encodeURIComponent(db.name)}">{db.name}</a>
                <Badge>{t(`db.engine.${db.engine}`)}{db.version ? ` ${db.version}` : ''}</Badge>
            <Badge tone={stateTone(db.status.state)} dot>{t(`db.state.${db.status.state}`)}</Badge>
            {#if db.declared}<Badge tone="muted">deployment.yaml</Badge>{/if}
              </div>
              {#if all}
                <a class="project small muted" href="#/projects/{db.project.slug}/databases">{db.project.name}</a>
              {/if}
            </div>
          </div>
          <div class="buttons">
            <Button size="sm" variant="primary" onclick={() => navigate(`/databases/${slugOf(db)}/${encodeURIComponent(db.name)}`)}>{t('dbs.explore')}</Button>
            {#if db.engine !== 'sqlite'}
              <Button size="sm" onclick={() => openLogs(db)}>{t('dbs.logs')}</Button>
            {/if}
            {#if db.engine !== 'sqlite'}
              <Button size="sm" pending={busy[keyOf(db)] === 'restart'} onclick={() => restart(db)}>
                {t('db.restart')}
              </Button>
            {/if}
            <Button
              size="sm"
              pending={busy[keyOf(db)] === 'backup'}
              disabled={!targets.length}
              onclick={() => backupNow(db)}
            >
              {t('db.backupNow')}
            </Button>
            <Button size="sm" onclick={() => openBackups(db)}>{t('db.backups')}</Button>
            <Button size="sm" onclick={() => openSchedule(db)} disabled={!targets.length}>
              {t('db.schedule')}
            </Button>
            <Button size="sm" variant="danger" onclick={() => ((removing = db), (deleteData = false))}>
              {t('common.delete')}
            </Button>
          </div>
        </header>
        {#if db.status.detail}<p class="small warn">{db.status.detail}</p>{/if}

        {#if all}
          <p class="vars mono small">
            {#each Object.keys(db.env).filter((k) => k.endsWith('_URL') || k.endsWith('_URI')).sort() as key}
              <span class="key">{key}</span>
            {/each}
            {#if db.host}<span class="faint">{db.host}:{db.port}</span>{/if}
          </p>
        {:else}
          <table class="env">
            <tbody>
              {#each Object.entries(db.env).sort() as [key, value]}
                <tr><td class="mono key">{key}</td><td class="mono faint">{value}</td></tr>
              {/each}
            </tbody>
          </table>
        {/if}

        <p class="small muted">
          {#if db.backup}
            {t('db.scheduled', { schedule: db.backup.schedule, target: db.backup.target, keep: db.backup.keep })}
            {#if db.nextBackup}· {t('db.next', { when: exact(db.nextBackup) })}{/if}
          {:else if targets.length}
            {t('db.noSchedule')}
          {:else}
            {t('db.noTargets')} <a href="#/settings/backups">{t('db.addTarget')}</a>
          {/if}
          {#if db.backups?.length}
            · {t('db.last', { status: t(`db.status.${db.backups[0].status}`), when: age(db.backups[0].startedAt) })}
          {/if}
        </p>
      </section>
    {/each}
  {/if}
</Card>

{#if !all}
<Card title={t('db.declareTitle')}>
  <p class="muted small">{t('db.declareBlurb')}</p>
  <pre class="mono">{`databases:
  - postgres               # DATABASE_URL, DATABASE_HOST, …

  - engine: redis          # REDIS_URL
    name: cache

  - engine: mongo          # MONGODB_URL, MONGODB_URI
    version: "8.2"

env:
  # map a database onto the name your app expects
  PRISMA_URL: \${db.DATABASE_URL}`}</pre>
  <p class="muted small">{t('db.declareNote')}</p>
</Card>
{/if}

{#if adding}
  <Modal title={t('db.addTitle')} onclose={() => (adding = false)}>
    <div class="form">
      {#if all}
        <Field label={t('dbs.project')}>
          {#snippet children(id)}
            <select {id} bind:value={addProject}>
              {#each projects as p (p.id)}<option value={p.slug}>{p.name}</option>{/each}
            </select>
          {/snippet}
        </Field>
      {/if}
      <div class="engines" role="radiogroup">
        {#each engines as e}
          <button type="button" class="engine" class:active={engine === e} onclick={() => (engine = e)}>
            <span class="engine-head"><DbIcon engine={e} size={16} tile={false} /><strong>{t(`db.engine.${e}`)}</strong></span>
            <span class="small muted">{t(`db.engineHint.${e}`)}</span>
          </button>
        {/each}
      </div>
      <Field label={t('db.name')} hint={t('db.nameHint')}>
        {#snippet children(id)}
          <input {id} bind:value={name} placeholder={engine === 'redis' ? 'cache' : 'main'} autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>
      {#if engine !== 'sqlite'}
        <Field label={t('db.version')} hint={t('db.versionHint')}>
          {#snippet children(id)}
            <input {id} bind:value={version} placeholder={{ postgres: '17', mysql: '8.4', mongo: '8.2', redis: '7' }[engine]} autocomplete="off" spellcheck="false" />
          {/snippet}
        </Field>
      {/if}
      <Field label={t('db.prefix')} hint={t('db.prefixHint')}>
        {#snippet children(id)}
          <input {id} bind:value={envPrefix} placeholder={engine === 'redis' ? 'REDIS' : engine === 'mongo' ? 'MONGODB' : 'DATABASE'} autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>
      <p class="small muted">{t('db.addNote')}</p>
      {#if addError}
        <div class="failed small" role="alert">
          <strong>{addError.message}</strong>
          {#if addError.details.length}
            <ul>{#each addError.details as d}<li>{d}</li>{/each}</ul>
          {/if}
        </div>
      {/if}
    </div>
    {#snippet footer()}
      <Button variant="ghost" onclick={() => (adding = false)}>{t('common.cancel')}</Button>
      <Button variant="primary" pending={saving} disabled={all && !addProject} onclick={add}>{t('db.create')}</Button>
    {/snippet}
  </Modal>
{/if}

{#if scheduling}
  <Modal title={t('db.scheduleTitle', { name: scheduling.name })} onclose={() => (scheduling = null)}>
    <div class="form">
      <Field label={t('db.cron')} hint={t('db.cronHint')}>
        {#snippet children(id)}
          <input {id} bind:value={schedule} placeholder="0 3 * * *" autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>
      <Field label={t('db.target')}>
        {#snippet children(id)}
          <select {id} bind:value={target}>
            {#each targets as tg}<option value={tg.name}>{tg.name} — {tg.location}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <Field label={t('db.keep')} hint={t('db.keepHint')}>
        {#snippet children(id)}
          <input {id} type="number" min="0" bind:value={keep} />
        {/snippet}
      </Field>
    </div>
    {#snippet footer()}
      {#if scheduling.backup}
        <Button variant="danger" onclick={() => saveSchedule(true)}>{t('db.turnOff')}</Button>
      {/if}
      <Button variant="ghost" onclick={() => (scheduling = null)}>{t('common.cancel')}</Button>
      <Button variant="primary" disabled={!schedule.trim() || !target} onclick={() => saveSchedule()}>
        {t('common.save')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if backupsOf}
  <Modal title={t('db.backupsTitle', { name: backupsOf.name })} onclose={() => (backupsOf = null)}>
    {#if backups === null}
      <p class="muted small">{t('common.loading')}</p>
    {:else}
      {#if backups.error}<p class="small warn">{backups.error}</p>{/if}
      {#if backups.files.length === 0}
        <p class="muted small">{t('db.noBackups')}</p>
      {:else}
        <table class="table">
          <tbody>
            {#each backups.files as f (f.name)}
              <tr>
                <td class="mono small">{f.name}</td>
                <td class="faint small">{bytes(f.size)}</td>
                <td class="faint small">{f.modified ? age(f.modified) : ''}</td>
                <td class="actions">
                  <Button size="sm" variant="danger" onclick={() => (restoring = { db: backupsOf, file: f.name })}>
                    {t('db.restore')}
                  </Button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
      {#if backups.history.length}
        <h4>{t('db.history')}</h4>
        <ul class="history small">
          {#each backups.history as h (h.id)}
            <li>
              <Badge tone={h.status === 'succeeded' ? 'good' : h.status === 'failed' ? 'bad' : 'busy'}>
                {t(`db.kind.${h.kind}`)} · {t(`db.status.${h.status}`)}
              </Badge>
              <span class="mono">{h.file}</span>
              <span class="faint">{age(h.startedAt)} · {h.target}</span>
              {#if h.error}<div class="warn">{h.error}</div>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </Modal>
{/if}

{#if logsOf}
  <Modal title={t('dbs.logsTitle', { name: logsOf.name })} wide onclose={() => (logsOf = null)}>
    {#if logLines === null}
      <p class="muted small">{t('common.loading')}</p>
    {:else}
      <LogView lines={logLines} />
    {/if}
  </Modal>
{/if}

{#if restoring}
  <Confirm
    title={t('db.restoreTitle', { name: restoring.db.name })}
    message={t('db.restoreMessage', { file: restoring.file })}
    confirmLabel={t('db.restore')}
    confirmText={restoring.db.name}
    danger
    onconfirm={restore}
    onclose={() => (restoring = null)}
  />
{/if}

{#if removing}
  <Confirm
    title={t('db.deleteTitle', { name: removing.name })}
    message={t('db.deleteMessage', { volume: removing.volume })}
    confirmLabel={t('common.delete')}
    confirmText={deleteData ? removing.name : ''}
    danger
    onconfirm={remove}
    onclose={() => (removing = null)}
  >
    <label class="check">
      <input type="checkbox" bind:checked={deleteData} />
      <span>{t('db.deleteData')}</span>
    </label>
  </Confirm>
{/if}

<style>
  .pad { padding: 16px; }
  .db { padding: 14px 16px; border-top: 1px solid var(--border); }
  .db:first-of-type { border-top: none; }
  header { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; justify-content: space-between; }
  .head { display: flex; gap: 10px; align-items: center; min-width: 0; }
  .title { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .line { display: flex; gap: 7px; align-items: center; flex-wrap: wrap; }
  .name { font-weight: 650; color: var(--text); }
  .project { color: var(--text-muted); }
  .engine-head { display: flex; gap: 6px; align-items: center; }
  .buttons { display: flex; gap: 6px; flex-wrap: wrap; }
  .env { width: 100%; margin: 10px 0 6px; border-collapse: collapse; font-size: 12px; }
  .env td { padding: 3px 8px 3px 0; vertical-align: top; overflow-wrap: anywhere; }
  .env .key { white-space: nowrap; width: 1%; color: var(--text); }
  .vars { display: flex; flex-wrap: wrap; gap: 6px 12px; margin: 8px 0 4px; }
  .vars .key { color: var(--text); }
  .warn { color: var(--warn); }
  .form { display: flex; flex-direction: column; gap: 14px; }
  .engines { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 8px; }
  .engine {
    display: flex; flex-direction: column; gap: 2px; text-align: left;
    padding: 9px 11px; border: 1px solid var(--border); border-radius: var(--radius-sm);
    background: var(--bg-raised); color: var(--text); cursor: pointer;
  }
  .engine.active { border-color: var(--accent); background: var(--accent-bg); }
  .failed { padding: 9px 11px; background: var(--bad-bg); border-left: 3px solid var(--bad); border-radius: var(--radius-sm); }
  .failed ul { margin: 4px 0 0; padding-left: 17px; }
  .check { display: flex; gap: 9px; align-items: flex-start; font-size: 13px; margin-top: 10px; }
  .history { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 8px; }
  .history li { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  h4 { margin: 16px 0 8px; font-size: 13px; }
  pre {
    margin: 0 0 10px; padding: 11px 13px; background: var(--bg-sunken);
    border: 1px solid var(--border); border-radius: var(--radius-sm);
    font-size: 12.5px; line-height: 1.6; overflow-x: auto;
  }
  :global(.card + .card) { margin-top: 14px; }
</style>
