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
  let description = $state('');
  let readOnly = $state(false);
  let create = $state(true);
  let saving = $state(false);

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

  function openAdd() {
    name = '';
    path = '';
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

<Card
  title="Shared volumes"
  description="Host directories you make available to projects."
>
  {#snippet actions()}
    <Button size="sm" variant="primary" onclick={openAdd}>Register volume</Button>
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
      <code>disk0</code> and neither can reach the other's files.
    </p>
  </div>

  {#if settings === null}
    <p class="muted small">Loading…</p>
  {:else if settings.sharedVolumes.length === 0}
    <Empty
      title="No shared volumes"
      description="Register a directory and projects can persist data across deployments — uploads, caches, databases."
    >
      <Button variant="primary" onclick={openAdd}>Register a volume</Button>
    </Empty>
  {:else}
    <ul class="vols">
      {#each settings.sharedVolumes as vol (vol.name)}
        <li>
          <div class="grow">
            <div class="row wrap">
              <code class="vname">{vol.name}</code>
              {#if vol.readOnly}<Badge tone="muted">Read-only</Badge>{/if}
              {#if vol.error}<Badge tone="bad">{vol.error}</Badge>{/if}
            </div>
            <div class="paths small">
              <span class="faint">host</span>
              <code>{vol.path}/&lt;project id&gt;</code>
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
      {/each}
    </ul>
  {/if}
</Card>

<Card title="Using a volume in a project">
  <pre class="mono">{`volumes:
  - disk0                  # mounts at /shared/disk0

  - name: uploads
    mountPath: /var/data   # somewhere else
    readOnly: false

  - name: disk0
    subPath: cache         # a subdirectory of this project's own area`}</pre>
  <p class="muted small">
    Asking for a volume the server has not registered fails the deploy with a message naming it,
    rather than starting the app with a missing directory.
  </p>
</Card>

{#if adding}
  <Modal title="Register a shared volume" onclose={() => (adding = false)}>
    <div class="form">
      <Field label="Name" hint="What projects write in deployment.yaml." required>
        {#snippet children(id)}
          <input {id} bind:value={name} placeholder="disk0" autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <Field
        label="Host path"
        hint="publix creates one subdirectory per project inside it. Never point this at a system directory."
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
        <div class="preview small">
          A project with ID <code>abcd1234</code> would see
          <code>{path.trim()}/abcd1234</code> at <code>/shared/{name.trim()}</code>.
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
