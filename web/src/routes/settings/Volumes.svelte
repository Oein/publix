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
      notify.success(`Registered ${name.trim()}`);
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
      notify.success(`Unregistered ${removing.name} — its data was left on disk.`);
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
        {#if vol.readOnly}<Badge tone="muted">Read-only</Badge>{/if}
        {#if vol.error}<Badge tone="bad">{vol.error}</Badge>{/if}
      </div>
      <div class="paths small">
        <span class="faint">host</span>
        <code>{vol.example}</code>
        <span class="faint">→</span>
        <code>{vol.mount}</code>
      </div>
      {#if vol.description}<p class="muted small desc">{vol.description}</p>{/if}
      <div class="small faint used">
        {#if vol.usedBy.length}
          Used by {vol.usedBy.join(', ')}
        {:else}
          Not used by any project
        {/if}
      </div>
    </div>
    <Button size="sm" variant="danger" onclick={() => (removing = vol)}>Unregister</Button>
  </li>
{/snippet}

<Card
  title="Project volumes"
  description="Storage a project keeps to itself."
>
  {#snippet actions()}
    <Button size="sm" variant="primary" onclick={() => openAdd('project')}>Register volume</Button>
  {/snippet}

  <div class="explain">
    <p class="small">
      A project asks for a volume <em>by name</em> in its <code>deployment.yaml</code> — it never
      names a host path. publix mounts
      <code class="path">&lt;host path&gt;/&lt;project id&gt;</code>
      into the container at <code>/shared/&lt;name&gt;</code>.
    </p>
    <p class="small muted">
      The per-project subdirectory is the isolation boundary: two projects can both mount
      <code>disk0</code> and neither can reach the other's files. The project ID never changes,
      so renaming a project cannot orphan or expose its data.
    </p>
  </div>

  {#if settings === null}
    <p class="muted small">Loading…</p>
  {:else if projectVolumes.length === 0}
    <Empty
      title="No project volumes"
      description="Register a directory and projects can persist data across deployments — uploads, caches, databases — each in its own place."
    >
      <Button variant="primary" onclick={() => openAdd('project')}>Register a volume</Button>
    </Empty>
  {:else}
    <ul class="vols">
      {#each projectVolumes as vol (vol.name)}{@render volumeRow(vol)}{/each}
    </ul>
  {/if}
</Card>

<Card
  title="Shared volumes"
  description="One directory that every project mounting it reads and writes."
>
  {#snippet actions()}
    <Button size="sm" onclick={() => openAdd('shared')}>Register volume</Button>
  {/snippet}

  <div class="explain warn">
    <p class="small">
      Every project that mounts a shared volume gets the <em>same</em> directory —
      <code class="path">&lt;host path&gt;</code>, with no per-project subdirectory. That is the
      point: a media library, a dataset, a common cache.
    </p>
    <p class="small muted">
      It also means there is no isolation. Any project mounting it can read, overwrite or delete
      what another one wrote. Mark it read-only if the projects using it only need to read.
    </p>
  </div>

  {#if settings === null}
    <p class="muted small">Loading…</p>
  {:else if sharedVolumes.length === 0}
    <Empty
      title="No shared volumes"
      description="Register one when several projects genuinely need the same files. If they each need their own storage, use a project volume instead."
    >
      <Button onclick={() => openAdd('shared')}>Register a shared volume</Button>
    </Empty>
  {:else}
    <ul class="vols">
      {#each sharedVolumes as vol (vol.name)}{@render volumeRow(vol)}{/each}
    </ul>
  {/if}
</Card>

<Card title="Using a volume in a project">
  <p class="muted small">
    A project names a volume the same way whichever kind it is — the scope is the server's
    decision, not the repository's:
  </p>
  <pre class="mono">{`volumes:
  - disk0                  # mounts at /shared/disk0

  - name: media
    mountPath: /var/media  # somewhere else
    readOnly: true         # sensible for a shared library

  - name: disk0
    subPath: cache         # a subdirectory of what this project gets`}</pre>
  <p class="muted small">
    Asking for a volume the server has not registered fails the deploy with a message naming it,
    rather than starting the app with a missing directory.
  </p>
</Card>

{#if adding}
  <Modal
    title={scope === 'shared' ? 'Register a shared volume' : 'Register a project volume'}
    onclose={() => (adding = false)}
  >
    <div class="form">
      <Field label="Name" hint="What projects write in deployment.yaml." required>
        {#snippet children(id)}
          <input {id} bind:value={name} placeholder="disk0" autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <Field
        label="Host path"
        hint={scope === 'shared'
          ? 'Every project that mounts this volume gets this directory itself. Never point it at a system directory.'
          : 'publix creates one subdirectory per project inside it. Never point this at a system directory.'}
        required
      >
        {#snippet children(id)}
          <input {id} bind:value={path} placeholder="/mnt/data" autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <Field label="Description" hint="Shown here, to remind you what this is for.">
        {#snippet children(id)}
          <input {id} bind:value={description} placeholder="Bulk storage on the data disk" autocomplete="off" />
        {/snippet}
      </Field>

      <label class="check">
        <input type="checkbox" bind:checked={create} />
        <span>Create the directory if it does not exist</span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={readOnly} />
        <span>
          Mount read-only for every project
          <span class="small muted">Overrides whatever a project's deployment.yaml asks for.</span>
        </span>
      </label>

      {#if name.trim() && path.trim()}
        <div class="preview small" class:sharedPreview={scope === 'shared'}>
          {#if scope === 'shared'}
            <strong>Every</strong> project that mounts this sees
            <code>{path.trim()}</code> at <code>/shared/{name.trim()}</code> — the same files,
            with no isolation between them.
          {:else}
            A project with ID <code>abcd1234</code> would see
            <code>{path.trim()}/abcd1234</code> at <code>/shared/{name.trim()}</code>. Another
            project mounting the same volume gets its own directory.
          {/if}
        </div>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (adding = false)}>Cancel</Button>
      <Button variant="primary" pending={saving} disabled={!name.trim() || !path.trim()} onclick={add}>
        Register
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if removing}
  <Confirm
    title="Unregister {removing.name}?"
    message="Projects will no longer be able to mount it. Nothing on disk is deleted — {removing.path} and everything under it stays exactly as it is."
    confirmLabel="Unregister"
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
