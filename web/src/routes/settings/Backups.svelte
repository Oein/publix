<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import Badge from '../../lib/Badge.svelte';
  import Empty from '../../lib/Empty.svelte';
  import Modal from '../../lib/Modal.svelte';
  import Confirm from '../../lib/Confirm.svelte';
  import { t } from '../../lib/i18n.svelte.js';

  /**
   * Backup targets: where database backups go. A host directory, or
   * anywhere rclone reaches — which is how Google Drive, S3 and the rest
   * arrive without publix speaking any of their APIs.
   */
  let { revision } = $props();

  let targets = $state(null);
  let adding = $state(false);
  let removing = $state(null);
  let testing = $state(null);

  let name = $state('');
  let type = $state('local');
  let path = $state('');
  let remote = $state('gdrive:publix-backups');
  let rcloneConfig = $state('');
  let description = $state('');
  let saving = $state(false);
  let addError = $state(null);

  async function load() {
    try {
      targets = await api.backupTargets.list();
    } catch (err) {
      notify.error(err);
    }
  }

  $effect(() => {
    revision;
    load();
  });

  function openAdd(kind) {
    name = kind === 'rclone' ? 'gdrive' : 'disk';
    type = kind;
    path = '';
    remote = 'gdrive:publix-backups';
    rcloneConfig = '';
    description = '';
    addError = null;
    adding = true;
  }

  async function add() {
    saving = true;
    addError = null;
    try {
      targets = await api.backupTargets.add({
        name: name.trim(),
        type,
        path: type === 'local' ? path.trim() : '',
        remote: type === 'rclone' ? remote.trim() : '',
        rcloneConfig: type === 'rclone' ? rcloneConfig : '',
        description: description.trim(),
      });
      notify.success(t('bk.added', { name: name.trim() }));
      adding = false;
    } catch (err) {
      addError = { message: err.message, details: err.details ?? [] };
    } finally {
      saving = false;
    }
  }

  async function test(tg) {
    testing = tg.name;
    try {
      await api.backupTargets.test(tg.name);
      notify.success(t('bk.testOk', { name: tg.name }));
    } catch (err) {
      notify.error(err);
    } finally {
      testing = null;
    }
  }

  async function remove() {
    try {
      targets = await api.backupTargets.remove(removing.name);
      notify.success(t('bk.removed', { name: removing.name }));
      removing = null;
    } catch (err) {
      notify.error(err);
    }
  }
</script>

<Card title={t('bk.title')} description={t('bk.desc')} flush>
  {#snippet actions()}
    <Button size="sm" onclick={() => openAdd('local')}>{t('bk.addLocal')}</Button>
    <Button size="sm" variant="primary" onclick={() => openAdd('rclone')}>{t('bk.addRclone')}</Button>
  {/snippet}

  {#if targets === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if targets.length === 0}
    <Empty title={t('bk.noneTitle')} description={t('bk.noneDesc')}>
      <Button onclick={() => openAdd('local')}>{t('bk.addLocal')}</Button>
      <Button variant="primary" onclick={() => openAdd('rclone')}>{t('bk.addRclone')}</Button>
    </Empty>
  {:else}
    <div class="table-scroll">
      <table class="table">
        <thead>
          <tr>
            <th>{t('bk.name')}</th>
            <th>{t('bk.location')}</th>
            <th>{t('bk.usedBy')}</th>
            <th class="actions"></th>
          </tr>
        </thead>
        <tbody>
          {#each targets as tg (tg.name)}
            <tr>
              <td>
                <code>{tg.name}</code>
                <Badge tone="muted">{tg.type === 'local' ? t('bk.typeLocal') : 'rclone'}</Badge>
                {#if tg.description}<div class="sub">{tg.description}</div>{/if}
              </td>
              <td class="mono faint">{tg.location}</td>
              <td>{tg.usedBy.length ? tg.usedBy.join(', ') : '—'}</td>
              <td class="actions">
                <Button size="sm" pending={testing === tg.name} onclick={() => test(tg)}>{t('bk.test')}</Button>
                <Button size="sm" variant="danger" onclick={() => (removing = tg)}>{t('bk.remove')}</Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>

<Card title={t('bk.howTitle')}>
  <p class="muted small">{t('bk.howBlurb')}</p>
  <pre class="mono">{`<target>/<project id>/<database>/<project>-<database>-20261029-030000.dump`}</pre>
  <p class="muted small">{t('bk.gdriveHow')}</p>
  <pre class="mono">{`rclone config            # n → name: gdrive → storage: drive → follow the browser login
rclone config show gdrive # paste this output below`}</pre>
</Card>

{#if adding}
  <Modal title={type === 'local' ? t('bk.addLocalTitle') : t('bk.addRcloneTitle')} onclose={() => (adding = false)}>
    <div class="form">
      <Field label={t('bk.name')} hint={t('bk.nameHint')} required>
        {#snippet children(id)}
          <input {id} bind:value={name} autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>
      {#if type === 'local'}
        <Field label={t('bk.path')} hint={t('bk.pathHint')} required>
          {#snippet children(id)}
            <input {id} bind:value={path} placeholder="/mnt/backup" autocomplete="off" spellcheck="false" />
          {/snippet}
        </Field>
      {:else}
        <Field label={t('bk.remote')} hint={t('bk.remoteHint')} required>
          {#snippet children(id)}
            <input {id} bind:value={remote} autocomplete="off" spellcheck="false" />
          {/snippet}
        </Field>
        <Field label={t('bk.config')} hint={t('bk.configHint')} required>
          {#snippet children(id)}
            <textarea {id} rows="8" class="mono" bind:value={rcloneConfig} spellcheck="false"
              placeholder={'[gdrive]\ntype = drive\nscope = drive\ntoken = {"access_token":"…"}'}></textarea>
          {/snippet}
        </Field>
      {/if}
      <Field label={t('bk.description')}>
        {#snippet children(id)}
          <input {id} bind:value={description} autocomplete="off" />
        {/snippet}
      </Field>
      <p class="small muted">{t('bk.testNote')}</p>
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
      <Button variant="primary" pending={saving} disabled={!name.trim()} onclick={add}>{t('bk.save')}</Button>
    {/snippet}
  </Modal>
{/if}

{#if removing}
  <Confirm
    title={t('bk.removeTitle', { name: removing.name })}
    message={t('bk.removeMessage')}
    confirmLabel={t('bk.remove')}
    danger
    onconfirm={remove}
    onclose={() => (removing = null)}
  />
{/if}

<style>
  .pad { padding: 16px; }
  .sub { margin-top: 2px; font-size: 12px; color: var(--text-muted); }
  code {
    padding: 1px 5px; background: var(--bg-sunken); border-radius: 3px;
    font-family: var(--mono); font-size: 12px; margin-right: 6px;
  }
  .actions { white-space: nowrap; }
  .form { display: flex; flex-direction: column; gap: 14px; }
  textarea { width: 100%; font-size: 12px; }
  .failed { padding: 9px 11px; background: var(--bad-bg); border-left: 3px solid var(--bad); border-radius: var(--radius-sm); }
  .failed ul { margin: 4px 0 0; padding-left: 17px; overflow-wrap: anywhere; }
  pre {
    margin: 0 0 10px; padding: 11px 13px; background: var(--bg-sunken);
    border: 1px solid var(--border); border-radius: var(--radius-sm);
    font-size: 12.5px; line-height: 1.6; overflow-x: auto;
  }
  :global(.card + .card) { margin-top: 14px; }
</style>
