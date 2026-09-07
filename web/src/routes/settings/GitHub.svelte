<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import Badge from '../../lib/Badge.svelte';
  import Confirm from '../../lib/Confirm.svelte';
  import { t, tparts } from '../../lib/i18n.svelte.js';

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
        notify.success(t('gh.connectedToast', { login: status.login }));
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
      notify.success(t('gh.disconnected'));
      disconnecting = false;
      load();
    } catch (err) {
      notify.error(err);
    }
  }

  function copy(value, what) {
    navigator.clipboard?.writeText(value);
    notify.info(t('gh.copied', { what }));
  }
</script>

<Card title={t('gh.connection')}>
  {#snippet actions()}
    {#if status?.configured}
      <Button size="sm" variant="danger" onclick={() => (disconnecting = true)}>
        {t('gh.disconnect')}
      </Button>
    {/if}
  {/snippet}

  {#if status === null}
    <p class="muted small">{t('common.loading')}</p>
  {:else if status.configured && !status.error}
    <div class="connected">
      {#if status.avatar}<img src={status.avatar} alt="" width="34" height="34" />{/if}
      <div class="grow">
        <div class="row wrap">
          <strong>{status.login}</strong>
          <Badge tone="good" dot>{t('gh.connected')}</Badge>
          <Badge tone="muted">{status.mode === 'app' ? t('gh.app') : t('gh.token')}</Badge>
          {#if status.repositorySelection}
            <Badge tone={status.repositorySelection === 'all' ? 'muted' : 'warn'}>
              {status.repositorySelection === 'all'
                ? t('gh.repoAccessAll')
                : t('gh.repoAccessSelected')}
            </Badge>
          {/if}
        </div>
        <p class="small muted">{t('gh.connectedBlurb')}</p>
        {#if status.repositorySelection === 'selected'}
          <p class="small muted">{t('gh.selectedNote')}</p>
        {/if}
        {#if status.installationUrl}
          <p class="small">
            <a href={status.installationUrl} target="_blank" rel="noreferrer noopener">
              {t('gh.manageAccess')} ↗
            </a>
          </p>
        {/if}
      </div>
    </div>
  {:else if status.error}
    <div class="problem">
      <strong class="small">{t('gh.rejected')}</strong>
      <p class="small muted">{status.error}</p>
    </div>
  {/if}

  {#if !status?.configured || status?.error}
    <div class="modes">
      <button class:active={mode === 'token'} onclick={() => (mode = 'token')}>
        <strong>{t('gh.tokenMode')}</strong>
        <span class="small muted">{t('gh.tokenModeHint')}</span>
      </button>
      <button class:active={mode === 'app'} onclick={() => (mode = 'app')}>
        <strong>{t('gh.appMode')}</strong>
        <span class="small muted">{t('gh.appModeHint')}</span>
      </button>
    </div>
  {/if}

  <div class="form">
    {#if mode === 'token'}
      <Field label={t('gh.tokenLabel')} hint={t('gh.tokenHint')} required={!status?.configured}>
        {#snippet children(id)}
          <input
            {id}
            type="password"
            bind:value={token}
            placeholder={status?.configured ? t('gh.keepToken') : 'ghp_…'}
            autocomplete="off"
            spellcheck="false"
          />
        {/snippet}
      </Field>
    {:else}
      <Field label={t('gh.appId')} required>
        {#snippet children(id)}
          <input {id} bind:value={appId} placeholder="123456" autocomplete="off" />
        {/snippet}
      </Field>
      <Field label={t('gh.installationId')} hint={t('gh.installationIdHint')}>
        {#snippet children(id)}
          <input {id} bind:value={installationId} placeholder="12345678" autocomplete="off" />
        {/snippet}
      </Field>
      <Field
        label={t('gh.privateKey')}
        hint={t('gh.privateKeyHint')}
        required={!status?.configured}
      >
        {#snippet children(id)}
          <textarea
            {id}
            bind:value={privateKey}
            rows="5"
            spellcheck="false"
            placeholder={status?.configured
              ? t('gh.keepKey')
              : '-----BEGIN RSA PRIVATE KEY-----'}
          ></textarea>
        {/snippet}
      </Field>
    {/if}

    <Field label={t('gh.apiBase')} hint={t('gh.apiBaseHint')}>
      {#snippet children(id)}
        <input {id} bind:value={apiBase} placeholder="https://api.github.com" autocomplete="off" />
      {/snippet}
    </Field>

    <div>
      <Button variant="primary" pending={saving} onclick={connect}>
        {status?.configured ? t('gh.update') : t('gh.connect')}
      </Button>
    </div>
  </div>
</Card>

<Card title={t('gh.webhook')} description={t('gh.webhookDesc')}>
  {#if status}
    <div class="field">
      <label for="hookurl">{t('gh.payloadUrl')}</label>
      <div class="hook">
        <code id="hookurl" class="grow truncate">{status.webhookUrl}</code>
        <Button size="sm" onclick={() => copy(status.webhookUrl, t('gh.url'))}>
          {t('common.copy')}
        </Button>
      </div>
    </div>

    <div class="field">
      <label for="hooksecret">{t('gh.secret')}</label>
      <div class="hook">
        <code id="hooksecret" class="grow truncate">
          {showSecret ? status.webhookSecret : '•'.repeat(48)}
        </code>
        <Button size="sm" variant="ghost" onclick={() => (showSecret = !showSecret)}>
          {showSecret ? t('gh.hide') : t('gh.reveal')}
        </Button>
        <Button size="sm" onclick={() => copy(status.webhookSecret, t('gh.secret'))}>
          {t('common.copy')}
        </Button>
      </div>
    </div>

    {#if !status.publicUrlSet}
      <p class="warn small">{t('gh.noPublicUrl')}</p>
    {/if}

    {#if status.mode === 'app'}
      <div class="note" class:ok={status.appDeliversWebhooks}>
        {#if status.appDeliversWebhooks}
          <strong class="small">{t('gh.appDelivers')}</strong>
          <p class="small muted">{t('gh.appDeliversBlurb')}</p>
        {:else}
          <strong class="small">{t('gh.setAppWebhook')}</strong>
          <p class="small muted">
            {#each tparts('gh.setAppWebhookBlurb') as part}{#if part.slot}<em
                >{t('gh.callbackUrlEm')}</em
              >{:else}{part.text}{/if}{/each}
          </p>
          {#if status.appWebhookUrl}
            <p class="small muted">
              {#each tparts('gh.appPostsTo', {
                inactive: status.appWebhookActive ? '' : t('gh.inactive'),
              }) as part}{#if part.slot}<code>{status.appWebhookUrl}</code
                >{:else}{part.text}{/if}{/each}
            </p>
          {:else}
            <p class="small muted">
              {#each tparts('gh.appNoWebhook') as part}{#if part.slot}<em>{t('gh.webhooksPerm')}</em
                >{:else}{part.text}{/if}{/each}
            </p>
          {/if}
        {/if}
      </div>
    {:else}
      <p class="muted small">{t('gh.tokenWebhookBlurb')}</p>
    {/if}
  {/if}
</Card>

<Card title={t('gh.setupTitle')} description={t('gh.setupDesc')}>
  <dl>
    <div>
      <dt>{t('gh.homepageUrl')}</dt>
      <dd>
        <code
          >{status?.webhookUrl?.replace('/api/webhooks/github', '') ??
            'https://publix.example.com'}</code
        >
        <span class="muted">{t('gh.homepageNote')}</span>
      </dd>
    </div>
    <div>
      <dt>{t('gh.callbackUrl')}</dt><dd><span class="muted">{t('gh.callbackNote')}</span></dd>
    </div>
    <div><dt>{t('gh.webhookUrl')}</dt><dd><code>{status?.webhookUrl ?? ''}</code></dd></div>
    <div>
      <dt>{t('gh.webhookSecret')}</dt><dd><span class="muted">{t('gh.webhookSecretNote')}</span></dd>
    </div>
    <div>
      <dt>{t('gh.subscribeTo')}</dt>
      <dd><code>Push</code> <span class="muted">{t('gh.subscribeNote')}</span></dd>
    </div>
  </dl>

  <p class="small muted heading">{t('gh.repoPerms')}</p>
  <dl>
    <div>
      <dt>{t('gh.contents')}</dt>
      <dd>{t('gh.readOnly')} <span class="muted">{t('gh.contentsNote')}</span></dd>
    </div>
    <div>
      <dt>{t('gh.metadata')}</dt>
      <dd>{t('gh.readOnly')} <span class="muted">{t('gh.metadataNote')}</span></dd>
    </div>
    <div>
      <dt>{t('gh.commitStatuses')}</dt>
      <dd>{t('gh.readWrite')} <span class="muted">{t('gh.commitStatusesNote')}</span></dd>
    </div>
    <div>
      <dt>{t('gh.webhooks')}</dt>
      <dd>{t('gh.readWrite')} <span class="muted">{t('gh.webhooksNote')}</span></dd>
    </div>
  </dl>
</Card>

{#if disconnecting}
  <Confirm
    title={t('gh.disconnectTitle')}
    message={t('gh.disconnectMessage')}
    confirmLabel={t('gh.disconnect')}
    danger
    onconfirm={disconnect}
    onclose={() => (disconnecting = false)}
  />
{/if}

<style>
  /* align-items: flex-start, because the account block can now run to
     several lines and a centred avatar would float in the middle of them. */
  .connected { display: flex; align-items: flex-start; gap: 12px; margin-bottom: 16px; }
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
