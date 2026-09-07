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
  import { t, tparts } from '../../lib/i18n.svelte.js';

  /**
   * Shared volumes.
   *
   * The screen leads with the isolation model, because that is the part an
   * operator has to trust before handing projects a host directory: a
   * project names a volume, never a path, and receives its own
   * subdirectory inside it.
   */
  let { revision } = $props();

  let settings = $state(null);
  let adding = $state(false);
  let removing = $state(null);

  let name = $state('');
  let path = $state('');
  let scope = $state('project');
  let description = $state('');
  let readOnly = $state(false);
  let create = $state(true);
  let saving = $state(false);
  // The server is the authority on what a volume may point at, and its
  // refusal belongs next to the field that caused it rather than in a
  // toast in the corner while the form is still open.
  let addError = $state(null);

  const projectVolumes = $derived((settings?.volumes ?? []).filter((v) => v.scope !== 'shared'));
  const sharedVolumes = $derived((settings?.volumes ?? []).filter((v) => v.scope === 'shared'));

  async function load() {
    try {
      settings = await api.settings.get();
    } catch (err) {
      notify.error(err);
    }
  }

  $effect(() => {
    revision;
    load();
  });

  function openAdd(kind) {
    name = '';
    path = '';
    scope = kind;
    description = '';
    readOnly = false;
    create = true;
    addError = null;
    adding = true;
  }

  async function add() {
    saving = true;
    addError = null;
    try {
      settings = await api.settings.addVolume({
        name: name.trim(),
        path: path.trim(),
        scope,
        description: description.trim(),
        readOnly,
        create,
      });
      notify.success(t('vol.registered', { name: name.trim() }));
      adding = false;
    } catch (err) {
      // The server answers with a headline and a line per broken rule, and
      // the rules are the part that says what to change.
      addError = { message: err.message, details: err.details ?? [] };
    } finally {
      saving = false;
    }
  }

  async function remove() {
    try {
      settings = await api.settings.removeVolume(removing.name);
      notify.success(t('vol.unregistered', { name: removing.name }));
      removing = null;
    } catch (err) {
      notify.error(err);
    }
  }
</script>

{#snippet volumeTable(rows)}
  <div class="table-scroll">
    <table class="table">
      <thead>
        <tr>
          <th>{t('vol.name')}</th>
          <th>{t('vol.colHost')}</th>
          <th>{t('vol.colMount')}</th>
          <th>{t('domainsPage.colProjects')}</th>
          <th class="actions"></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as vol (vol.name)}
          <tr>
            <td>
              <div class="cell">
                <code>{vol.name}</code>
                {#if vol.readOnly}<Badge tone="muted">{t('vol.readOnly')}</Badge>{/if}
                {#if vol.error}<Badge tone="bad">{vol.error}</Badge>{/if}
              </div>
              {#if vol.description}<div class="sub">{vol.description}</div>{/if}
            </td>
            <td class="faint mono">{vol.example}</td>
            <td class="faint mono">{vol.mount}</td>
            <td>
              {#if vol.usedBy.length}
                <span class="truncate" title={vol.usedBy.join(', ')}>{vol.usedBy.join(', ')}</span>
              {:else}
                <span class="faint">—</span>
              {/if}
            </td>
            <td class="actions">
              <Button size="sm" variant="danger" onclick={() => (removing = vol)}>
                {t('vol.unregister')}
              </Button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/snippet}

<Card title={t('vol.projectTitle')} description={t('vol.projectDesc')} flush>
  {#snippet actions()}
    <Button size="sm" variant="primary" onclick={() => openAdd('project')}>
      {t('vol.register')}
    </Button>
  {/snippet}

  {#if settings === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if projectVolumes.length === 0}
    <Empty title={t('vol.noProjectTitle')} description={t('vol.noProjectDesc')}>
      <Button variant="primary" onclick={() => openAdd('project')}>
        {t('vol.registerAVolume')}
      </Button>
    </Empty>
  {:else}
    {@render volumeTable(projectVolumes)}
  {/if}
</Card>

<Card title={t('vol.sharedTitle')} description={t('vol.sharedDesc')} flush>
  {#snippet actions()}
    <Button size="sm" onclick={() => openAdd('shared')}>{t('vol.register')}</Button>
  {/snippet}

  {#if settings === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if sharedVolumes.length === 0}
    <Empty title={t('vol.noSharedTitle')} description={t('vol.noSharedDesc')}>
      <Button onclick={() => openAdd('shared')}>{t('vol.registerAShared')}</Button>
    </Empty>
  {:else}
    {@render volumeTable(sharedVolumes)}
  {/if}
</Card>

<Card title={t('vol.usingTitle')}>
  <p class="muted small">{t('vol.usingBlurb')}</p>
  <pre class="mono">{`volumes:
  - disk0                  # mounts at /shared/disk0

  - name: media
    mountPath: /var/media  # somewhere else
    readOnly: true         # sensible for a shared library

  - name: disk0
    subPath: cache         # a subdirectory of what this project gets`}</pre>
  <p class="muted small">{t('vol.usingNote')}</p>
</Card>

{#if adding}
  <Modal
    title={scope === 'shared' ? t('vol.addSharedTitle') : t('vol.addProjectTitle')}
    onclose={() => (adding = false)}
  >
    <div class="form">
      <Field label={t('vol.name')} hint={t('vol.nameHint')} required>
        {#snippet children(id)}
          <input {id} bind:value={name} placeholder="disk0" autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <Field
        label={t('vol.path')}
        hint={scope === 'shared' ? t('vol.pathHintShared') : t('vol.pathHintProject')}
        required
      >
        {#snippet children(id)}
          <input {id} bind:value={path} placeholder="/mnt/data" autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <Field label={t('vol.description')} hint={t('vol.descriptionHint')}>
        {#snippet children(id)}
          <input
            {id}
            bind:value={description}
            placeholder={t('vol.descriptionPlaceholder')}
            autocomplete="off"
          />
        {/snippet}
      </Field>

      <label class="check">
        <input type="checkbox" bind:checked={create} />
        <span>{t('vol.create')}</span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={readOnly} />
        <span>
          {t('vol.mountReadOnly')}
          <span class="small muted">{t('vol.mountReadOnlyHint')}</span>
        </span>
      </label>

      {#if addError}
        <div class="failed small" role="alert">
          <strong>{t('vol.rejected')}</strong>
          {#if addError.details.length}
            <ul>
              {#each addError.details as detail}<li>{detail}</li>{/each}
            </ul>
          {:else}
            <p>{addError.message}</p>
          {/if}
        </div>
      {/if}

      <!-- The preview describes what registering would do, so it must not
           keep promising that after the server refused. -->
      {#if !addError && name.trim() && path.trim()}
        <div class="preview small" class:sharedPreview={scope === 'shared'}>
          {#if scope === 'shared'}
            {#each tparts('vol.previewShared') as part}{#if part.slot === 'every'}<strong
                >{t('vol.every')}</strong
              >{:else if part.slot === 'path'}<code>{path.trim()}</code
              >{:else if part.slot === 'mount'}<code>/shared/{name.trim()}</code
              >{:else}{part.text}{/if}{/each}
          {:else}
            {#each tparts('vol.previewProject') as part}{#if part.slot === 'id'}<code
                >abcd1234</code
              >{:else if part.slot === 'path'}<code>{path.trim()}/abcd1234</code
              >{:else if part.slot === 'mount'}<code>/shared/{name.trim()}</code
              >{:else}{part.text}{/if}{/each}
          {/if}
        </div>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (adding = false)}>{t('common.cancel')}</Button>
      <Button variant="primary" pending={saving} disabled={!name.trim() || !path.trim()} onclick={add}>
        {t('vol.registerButton')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if removing}
  <Confirm
    title={t('vol.unregisterTitle', { name: removing.name })}
    message={t('vol.unregisterMessage', { path: removing.path })}
    confirmLabel={t('vol.unregister')}
    danger
    onconfirm={remove}
    onclose={() => (removing = null)}
  />
{/if}

<style>
  .cell {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
  }

  /* A name is an identifier, not prose: it may not be broken mid-word to
     make room for the long paths beside it. Those wrap instead. */
  .cell code {
    white-space: nowrap;
    overflow-wrap: normal;
  }

  .sub {
    margin-top: 2px;
    font-size: 12px;
    color: var(--text-muted);
  }

  .pad { padding: 16px; }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 12px;
    overflow-wrap: anywhere;
  }

  .form { display: flex; flex-direction: column; gap: 14px; }

  .failed {
    padding: 9px 11px;
    background: var(--bad-bg);
    border-left: 3px solid var(--bad);
    border-radius: var(--radius-sm);
  }
  .failed strong { display: block; margin-bottom: 3px; }
  .failed p { margin: 0; white-space: pre-line; line-height: 1.5; }
  /* One line per rule the registration broke, which is what says what to
     change. Word-break because the offending value is usually a long path. */
  .failed ul {
    margin: 0;
    padding-left: 17px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .check { display: flex; align-items: flex-start; gap: 9px; cursor: pointer; font-size: 13px; }
  .check input { margin-top: 2px; flex: none; }
  .check span { display: flex; flex-direction: column; gap: 1px; }

  /* The preview is the isolation model made concrete, so a shared volume's
     is coloured as the caution it is rather than matching the other one. */
  .preview {
    padding: 9px 11px;
    background: var(--accent-bg);
    color: var(--accent-text);
    border-radius: var(--radius-sm);
    line-height: 1.6;
  }
  .preview.sharedPreview { background: var(--warn-bg); color: var(--warn); }
  .preview code { background: var(--bg-raised); }

  pre {
    margin: 0 0 10px;
    padding: 11px 13px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
    line-height: 1.6;
    overflow-x: auto;
  }

  :global(.card + .card) { margin-top: 14px; }
</style>
