<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import Badge from '../../lib/Badge.svelte';
  import Confirm from '../../lib/Confirm.svelte';

  let { revision } = $props();

  let status = $state(null);
  let mode = $state('token');
  let token = $state('');
  let appId = $state('');
  let installationId = $state('');
  let privateKey = $state('');
  let apiBase = $state('');
  let saving = $state(false);
  let disconnecting = $state(false);

  async function load() {
    try {
      status = await api.github.status();
      if (status.mode && status.mode !== 'none') mode = status.mode;
      if (status.apiBase && status.apiBase !== 'https://api.github.com') apiBase = status.apiBase;
    } catch (err) {
      notify.error(err);
    }
  }

  $effect(() => {
    revision;
    load();
  });

  async function connect() {
    saving = true;
    try {
      status = await api.github.connect(
        mode === 'token'
          ? { token: token.trim(), apiBase: apiBase.trim() }
          : {
              appId: appId.trim(),
              installationId: installationId.trim(),
              privateKey: privateKey.trim(),
              apiBase: apiBase.trim(),
            }
      );
      token = '';
      privateKey = '';
      if (status.error) {
        notify.error(status.error);
      } else {
        notify.success(`Connected to GitHub as ${status.login}`);
      }
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }

  async function disconnect() {
    try {
      await api.github.disconnect();
      notify.success('Disconnected from GitHub');
      disconnecting = false;
      load();
    } catch (err) {
      notify.error(err);
    }
  }

  function copyWebhook() {
    navigator.clipboard?.writeText(status.webhookUrl);
    notify.info('Webhook URL copied');
  }
</script>

<Card title="GitHub connection">
  {#snippet actions()}
    {#if status?.configured}
      <Button size="sm" variant="danger" onclick={() => (disconnecting = true)}>Disconnect</Button>
    {/if}
  {/snippet}

  {#if status === null}
    <p class="muted small">Loading…</p>
  {:else if status.configured && !status.error}
    <div class="connected">
      {#if status.avatar}<img src={status.avatar} alt="" width="34" height="34" />{/if}
      <div class="grow">
        <div class="row">
          <strong>{status.login}</strong>
          <Badge tone="good" dot>Connected</Badge>
          <Badge tone="muted">{status.mode === 'app' ? 'GitHub App' : 'Access token'}</Badge>
        </div>
        <p class="small muted">
          Repositories are listed on the Import screen. Pushes deploy automatically for projects
          with a webhook.
        </p>
      </div>
    </div>
  {:else if status.error}
    <div class="problem">
      <strong class="small">GitHub rejected the stored credentials</strong>
      <p class="small muted">{status.error}</p>
    </div>
  {/if}

  {#if !status?.configured || status?.error}
    <div class="modes">
      <button class:active={mode === 'token'} onclick={() => (mode = 'token')}>
        <strong>Personal access token</strong>
        <span class="small muted">Fastest to set up. Sees whatever your account can see.</span>
      </button>
      <button class:active={mode === 'app'} onclick={() => (mode = 'app')}>
        <strong>GitHub App</strong>
        <span class="small muted">
          Right for an organisation: scoped per repository, tokens rotate on their own.
        </span>
      </button>
    </div>
  {/if}

  <div class="form">
    {#if mode === 'token'}
      <Field
        label="Access token"
        hint="A classic token needs the `repo` scope. A fine-grained token needs Contents (read), Metadata (read), Webhooks (read and write) and Commit statuses (write)."
        required={!status?.configured}
      >
        {#snippet children(id)}
          <input
            {id}
            type="password"
            bind:value={token}
            placeholder={status?.configured ? 'Leave empty to keep the current token' : 'ghp_…'}
            autocomplete="off"
            spellcheck="false"
          />
        {/snippet}
      </Field>
    {:else}
      <Field label="App ID" required>
        {#snippet children(id)}
          <input {id} bind:value={appId} placeholder="123456" autocomplete="off" />
        {/snippet}
      </Field>
      <Field
        label="Installation ID"
        hint="Optional. If the App has exactly one installation, publix finds it."
      >
        {#snippet children(id)}
          <input {id} bind:value={installationId} placeholder="12345678" autocomplete="off" />
        {/snippet}
      </Field>
      <Field label="Private key" hint="The PEM file GitHub gave you when you created the App." required={!status?.configured}>
        {#snippet children(id)}
          <textarea
            {id}
            bind:value={privateKey}
            rows="5"
            spellcheck="false"
            placeholder={status?.configured
              ? 'Leave empty to keep the current key'
              : '-----BEGIN RSA PRIVATE KEY-----'}
          ></textarea>
        {/snippet}
      </Field>
    {/if}

    <Field label="API base URL" hint="Only for GitHub Enterprise Server.">
      {#snippet children(id)}
        <input {id} bind:value={apiBase} placeholder="https://api.github.com" autocomplete="off" />
      {/snippet}
    </Field>

    <div>
      <Button variant="primary" pending={saving} onclick={connect}>
        {status?.configured ? 'Update connection' : 'Connect'}
      </Button>
    </div>
  </div>
</Card>

<Card title="Webhook" description="Where GitHub sends push events.">
  {#if status}
    <div class="hook">
      <code class="grow truncate">{status.webhookUrl}</code>
      <Button size="sm" onclick={copyWebhook}>Copy</Button>
    </div>
    {#if !status.publicUrlSet}
      <p class="warn small">
        No public URL is set, so this address was guessed from your browser. Set it under
        Settings → Server before importing, or webhooks cannot be registered.
      </p>
    {:else}
      <p class="muted small">
        publix registers this automatically when you import a repository with “Deploy on every
        push” enabled. Its signature is verified on every request, so an unsigned call is refused.
      </p>
    {/if}
  {/if}
</Card>

{#if disconnecting}
  <Confirm
    title="Disconnect GitHub?"
    message="Repository listing and deploy-on-push stop working. Existing projects keep their containers running, and webhooks stay on the repositories until the projects are deleted."
    confirmLabel="Disconnect"
    danger
    onconfirm={disconnect}
    onclose={() => (disconnecting = false)}
  />
{/if}

<style>
  .connected { display: flex; align-items: center; gap: 12px; }
  .connected img { border-radius: 50%; }
  .connected p { margin: 3px 0 0; max-width: 62ch; }

  .problem {
    padding: 10px 12px;
    margin-bottom: 14px;
    background: var(--bad-bg);
    border-left: 3px solid var(--bad);
    border-radius: var(--radius-sm);
  }
  .problem p { margin: 3px 0 0; }

  .modes {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
    margin-bottom: 16px;
  }
  @media (max-width: 620px) { .modes { grid-template-columns: 1fr; } }

  .modes button {
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    padding: 10px 12px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .modes button:hover { background: var(--bg-hover); }
  .modes button.active { border-color: var(--accent); background: var(--accent-bg); }
  .modes strong { font-size: 13px; font-weight: 600; }

  .form { display: flex; flex-direction: column; gap: 14px; }

  .hook {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
    margin-bottom: 8px;
  }
  .hook code { font-family: var(--mono); font-size: 12px; }

  .warn {
    padding: 8px 10px;
    background: var(--warn-bg);
    color: var(--warn);
    border-radius: var(--radius-sm);
    line-height: 1.5;
    margin: 0;
  }

  :global(.card + .card) { margin-top: 14px; }
</style>
