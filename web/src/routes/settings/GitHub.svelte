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
  let showSecret = $state(false);

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

  function copy(value, what) {
    navigator.clipboard?.writeText(value);
    notify.info(`${what} copied`);
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
    <div class="field">
      <label for="hookurl">Payload URL</label>
      <div class="hook">
        <code id="hookurl" class="grow truncate">{status.webhookUrl}</code>
        <Button size="sm" onclick={() => copy(status.webhookUrl, 'URL')}>Copy</Button>
      </div>
    </div>

    <div class="field">
      <label for="hooksecret">Secret</label>
      <div class="hook">
        <code id="hooksecret" class="grow truncate">
          {showSecret ? status.webhookSecret : '•'.repeat(48)}
        </code>
        <Button size="sm" variant="ghost" onclick={() => (showSecret = !showSecret)}>
          {showSecret ? 'Hide' : 'Reveal'}
        </Button>
        <Button size="sm" onclick={() => copy(status.webhookSecret, 'Secret')}>Copy</Button>
      </div>
    </div>

    {#if !status.publicUrlSet}
      <p class="warn small">
        No public URL is set, so this address was guessed from your browser. Set it under
        Settings → Server before importing, or webhooks cannot be registered.
      </p>
    {/if}

    {#if status.mode === 'app'}
      <div class="note" class:ok={status.appDeliversWebhooks}>
        {#if status.appDeliversWebhooks}
          <strong class="small">Your GitHub App delivers these webhooks.</strong>
          <p class="small muted">
            publix will not add a webhook to each repository, because the App already sends a
            push event for every repository it is installed on. Adding both would deliver each
            push twice.
          </p>
        {:else}
          <strong class="small">Set this URL and secret on your GitHub App</strong>
          <p class="small muted">
            Under the App's settings → Webhook. Its <em>Callback URL</em> is not used —
            publix has no OAuth login, so leave “Request user authorization (OAuth) during
            installation” unchecked.
          </p>
          {#if status.appWebhookUrl}
            <p class="small muted">
              The App currently posts to <code>{status.appWebhookUrl}</code>{status.appWebhookActive
                ? ''
                : ' (inactive)'}.
            </p>
          {:else}
            <p class="small muted">
              The App has no webhook configured. publix will fall back to creating one on each
              repository as you import it, which also works — it just needs the
              <em>Webhooks</em> repository permission.
            </p>
          {/if}
        {/if}
      </div>
    {:else}
      <p class="muted small">
        publix registers this on each repository as you import it, with “Deploy on every push”
        enabled. Its signature is verified on every request, so an unsigned call is refused.
      </p>
    {/if}
  {/if}
</Card>

<Card title="Setting up a GitHub App" description="What to put in each field.">
  <dl>
    <div><dt>Homepage URL</dt><dd><code>{status?.webhookUrl?.replace('/api/webhooks/github', '') ?? 'https://publix.example.com'}</code> <span class="muted">— required by GitHub, unused by publix</span></dd></div>
    <div><dt>Callback URL</dt><dd><span class="muted">leave empty; publix has no OAuth login</span></dd></div>
    <div><dt>Webhook URL</dt><dd><code>{status?.webhookUrl ?? ''}</code></dd></div>
    <div><dt>Webhook secret</dt><dd><span class="muted">the secret above</span></dd></div>
    <div><dt>Subscribe to</dt><dd><code>Push</code> <span class="muted">— nothing else is read</span></dd></div>
  </dl>

  <p class="small muted heading">Repository permissions</p>
  <dl>
    <div><dt>Contents</dt><dd>Read-only <span class="muted">— to clone. Read and write only if you want publix to commit a deployment.yaml for you on import.</span></dd></div>
    <div><dt>Metadata</dt><dd>Read-only <span class="muted">— mandatory; GitHub selects it for you</span></dd></div>
    <div><dt>Commit statuses</dt><dd>Read and write <span class="muted">— to report deploy results on the commit</span></dd></div>
    <div><dt>Webhooks</dt><dd>Read and write <span class="muted">— only if the App has no webhook of its own</span></dd></div>
  </dl>
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

  .field { margin-bottom: 12px; }
  .field label {
    display: block;
    margin-bottom: 4px;
    font-size: 12.5px;
    font-weight: 550;
  }

  .hook {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
  }
  .hook code { font-family: var(--mono); font-size: 12px; }

  .note {
    margin-top: 12px;
    padding: 10px 12px;
    background: var(--warn-bg);
    border-left: 3px solid var(--warn);
    border-radius: var(--radius-sm);
  }
  .note.ok { background: var(--good-bg); border-left-color: var(--good); }
  .note p { margin: 4px 0 0; line-height: 1.5; }

  dl { margin: 0; display: flex; flex-direction: column; gap: 7px; }
  dl div { display: flex; gap: 12px; font-size: 12.5px; align-items: baseline; }
  dt { min-width: 130px; flex: none; color: var(--text-muted); }
  dd { margin: 0; }
  dd code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }
  .heading { margin: 16px 0 8px; font-weight: 600; color: var(--text); }

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
