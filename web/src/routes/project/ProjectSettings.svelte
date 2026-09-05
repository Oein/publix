<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import { t } from '../../lib/i18n.svelte.js';

  let { project, onchange, ondelete } = $props();

  let name = $state('');
  let description = $state('');
  let branch = $state('');
  let rootDir = $state('');
  let specPath = $state('');
  let autoDeploy = $state(false);
  let paused = $state(false);
  let saving = $state(false);

  function reset() {
    name = project.name;
    description = project.description ?? '';
    branch = project.repo?.branch ?? '';
    rootDir = project.rootDir ?? '';
    specPath = project.specPath ?? '';
    autoDeploy = project.autoDeploy;
    paused = Boolean(project.paused);
  }

  $effect(() => {
    project.id;
    project.updatedAt;
    reset();
  });

  const changed = $derived(
    name !== project.name ||
      description !== (project.description ?? '') ||
      branch !== (project.repo?.branch ?? '') ||
      rootDir !== (project.rootDir ?? '') ||
      specPath !== (project.specPath ?? '') ||
      autoDeploy !== project.autoDeploy ||
      paused !== Boolean(project.paused)
  );

  async function save() {
    saving = true;
    try {
      await api.projects.update(project.id, {
        name,
        description,
        branch,
        rootDir,
        specPath,
        autoDeploy,
        paused,
      });
      notify.success(t('ps.updated'));
      onchange();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

<Card title={t('ps.project')}>
  <div class="form">
    <Field label={t('ps.name')} hint={t('ps.nameHint')}>
      {#snippet children(id)}<input {id} bind:value={name} autocomplete="off" />{/snippet}
    </Field>

    <Field label={t('ps.description')}>
      {#snippet children(id)}<input {id} bind:value={description} autocomplete="off" />{/snippet}
    </Field>

    {#if project.repo}
      <div class="two">
        <Field label={t('ps.branch')} hint={t('ps.branchHint')}>
          {#snippet children(id)}<input {id} bind:value={branch} autocomplete="off" />{/snippet}
        </Field>
        <Field label={t('ps.rootDir')} hint={t('ps.rootDirHint')}>
          {#snippet children(id)}
            <input {id} bind:value={rootDir} placeholder="apps/web" autocomplete="off" />
          {/snippet}
        </Field>
      </div>
    {/if}

    <Field label={t('ps.specPath')} hint={t('ps.specPathHint')}>
      {#snippet children(id)}
        <input {id} bind:value={specPath} placeholder="deployment.yaml" autocomplete="off" />
      {/snippet}
    </Field>

    <label class="check">
      <input type="checkbox" bind:checked={autoDeploy} />
      <span>
        <strong>{t('ps.deployOnPush')}</strong>
        <span class="small muted">{t('ps.deployOnPushHint')}</span>
      </span>
    </label>

    <label class="check">
      <input type="checkbox" bind:checked={paused} />
      <span>
        <strong>{t('ps.pause')}</strong>
        <span class="small muted">{t('ps.pauseHint')}</span>
      </span>
    </label>
  </div>

  {#if changed}
    <div class="save">
      <Button variant="primary" pending={saving} onclick={save}>{t('common.save')}</Button>
      <Button variant="ghost" onclick={reset}>{t('common.discard')}</Button>
    </div>
  {/if}
</Card>

<Card title={t('ps.identifiers')}>
  <dl>
    <div><dt>{t('ps.projectId')}</dt><dd><code>{project.id}</code></dd></div>
    <div><dt>{t('ps.slug')}</dt><dd><code>{project.slug}</code></dd></div>
  </dl>
  <p class="muted small note">{t('ps.idNote')}</p>
</Card>

<Card title={t('ps.danger')}>
  <div class="spread">
    <div>
      <strong class="small">{t('ps.deleteHeading')}</strong>
      <p class="muted small">{t('ps.deleteBlurb')}</p>
    </div>
    <Button variant="danger" onclick={ondelete}>{t('common.delete')}</Button>
  </div>
</Card>

<style>
  .form { display: flex; flex-direction: column; gap: 14px; }

  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
  @media (max-width: 620px) { .two { grid-template-columns: 1fr; } }

  .check { display: flex; align-items: flex-start; gap: 9px; cursor: pointer; }
  .check input { margin-top: 2px; flex: none; }
  .check span { display: flex; flex-direction: column; gap: 1px; }
  .check strong { font-size: 13px; font-weight: 550; }

  .save {
    display: flex;
    gap: 8px;
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }

  dl { margin: 0; display: flex; flex-direction: column; gap: 7px; }
  dl div { display: flex; gap: 12px; font-size: 13px; }
  dt { min-width: 90px; color: var(--text-muted); }
  dd { margin: 0; }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }

  .note { margin-top: 12px; max-width: 76ch; line-height: 1.55; }

  :global(.card + .card) { margin-top: 14px; }
</style>
