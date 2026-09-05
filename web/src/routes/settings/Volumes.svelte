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
    adding = true;
  }

  async function add() {
    saving = true;
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
      notify.error(err);
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

{#snippet volumeRow(vol)}
  <li>
    <div class="grow">
      <div class="row wrap">
        <code class="vname">{vol.name}</code>
        {#if vol.readOnly}<Badge tone="muted">{t('vol.readOnly')}</Badge>{/if}
        {#if vol.error}<Badge tone="bad">{vol.error}</Badge>{/if}
      </div>
      <div class="paths small">
        <span class="faint">{t('vol.host')}</span>
        <code>{vol.example}</code>
        <span class="faint">→</span>
        <code>{vol.mount}</code>
      </div>
      {#if vol.description}<p class="muted small desc">{vol.description}</p>{/if}
      <div class="small faint used">
        {#if vol.usedBy.length}
          {t('vol.usedBy', { list: vol.usedBy.join(', ') })}
        {:else}
          {t('vol.notUsed')}
        {/if}
      </div>
    </div>
    <Button size="sm" variant="danger" onclick={() => (removing = vol)}>
      {t('vol.unregister')}
    </Button>
  </li>
{/snippet}

<Card title={t('vol.projectTitle')} description={t('vol.projectDesc')}>
  {#snippet actions()}
    <Button size="sm" variant="primary" onclick={() => openAdd('project')}>
      {t('vol.register')}
    </Button>
  {/snippet}

  <div class="explain">
    <p class="small">
      {#each tparts('vol.projectExplain') as part}{#if part.slot === 'byName'}<em
          >{t('vol.byName')}</em
        >{:else if part.slot === 'file'}<code>deployment.yaml</code
        >{:else if part.slot === 'hostPath'}<code class="path"
          >{t('vol.tokenHostPath')}/{t('vol.tokenProjectId')}</code
        >{:else if part.slot === 'mount'}<code>/shared/{t('vol.tokenName')}</code
        >{:else}{part.text}{/if}{/each}
    </p>
    <p class="small muted">
      {#each tparts('vol.projectExplainNote') as part}{#if part.slot}<code>disk0</code
        >{:else}{part.text}{/if}{/each}
    </p>
  </div>

  {#if settings === null}
    <p class="muted small">{t('common.loading')}</p>
  {:else if projectVolumes.length === 0}
    <Empty title={t('vol.noProjectTitle')} description={t('vol.noProjectDesc')}>
      <Button variant="primary" onclick={() => openAdd('project')}>
        {t('vol.registerAVolume')}
      </Button>
    </Empty>
  {:else}
    <ul class="vols">
      {#each projectVolumes as vol (vol.name)}{@render volumeRow(vol)}{/each}
    </ul>
  {/if}
</Card>

<Card title={t('vol.sharedTitle')} description={t('vol.sharedDesc')}>
  {#snippet actions()}
    <Button size="sm" onclick={() => openAdd('shared')}>{t('vol.register')}</Button>
  {/snippet}

  <div class="explain warn">
    <p class="small">
      {#each tparts('vol.sharedExplain') as part}{#if part.slot === 'same'}<em
          >{t('vol.same')}</em
        >{:else if part.slot === 'hostPath'}<code class="path">{t('vol.tokenHostPath')}</code
        >{:else}{part.text}{/if}{/each}
    </p>
    <p class="small muted">{t('vol.sharedExplainNote')}</p>
  </div>

  {#if settings === null}
    <p class="muted small">{t('common.loading')}</p>
  {:else if sharedVolumes.length === 0}
    <Empty title={t('vol.noSharedTitle')} description={t('vol.noSharedDesc')}>
      <Button onclick={() => openAdd('shared')}>{t('vol.registerAShared')}</Button>
    </Empty>
  {:else}
    <ul class="vols">
      {#each sharedVolumes as vol (vol.name)}{@render volumeRow(vol)}{/each}
    </ul>
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

      {#if name.trim() && path.trim()}
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
  .explain {
    padding: 11px 13px;
    margin-bottom: 14px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
    border-left: 3px solid var(--accent);
  }
  /* A shared volume trades away isolation, so its explanation is marked
     as the caution it is rather than reading like the other one. */
  .explain.warn { border-left-color: var(--warn); background: var(--warn-bg); }
  .explain p { margin: 0 0 6px; line-height: 1.55; }
  .explain p:last-child { margin-bottom: 0; }

  .vols { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .vols li {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    padding: 11px 13px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
  }

  .vname { font-size: 13px; font-weight: 600; }
  .paths { display: flex; align-items: center; gap: 6px; margin-top: 5px; flex-wrap: wrap; }
  .desc { margin: 5px 0 0; }
  .used { margin-top: 4px; }

  code {
    padding: 1px 5px;
    background: var(--bg-raised);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }
  .explain code { background: var(--bg-raised); }

  .form { display: flex; flex-direction: column; gap: 14px; }

  .check { display: flex; align-items: flex-start; gap: 9px; cursor: pointer; font-size: 13px; }
  .check input { margin-top: 2px; flex: none; }
  .check span { display: flex; flex-direction: column; gap: 1px; }

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
