<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import Badge from '../../lib/Badge.svelte';
  import { t } from '../../lib/i18n.svelte.js';

  let { revision } = $props();

  let settings = $state(null);
  let system = $state(null);
  let form = $state({});
  let saving = $state(false);

  async function load() {
    try {
      settings = await api.settings.get();
      form = {
        appsDomain: settings.appsDomain ?? '',
        publicUrl: settings.publicUrl ?? '',
        network: settings.network,
        traefikDynamicDir: settings.traefikDynamicDir,
        entryPoints: settings.entryPoints.join(', '),
        certResolver: settings.certResolver ?? '',
        keepImages: settings.keepImages,
        keepDeployments: settings.keepDeployments,
        buildConcurrency: settings.buildConcurrency,
      };
    } catch (err) {
      notify.error(err);
    }
    try {
      system = await api.system();
    } catch {
      system = null;
    }
  }

  $effect(() => {
    revision;
    load();
  });

  async function save() {
    saving = true;
    try {
      settings = await api.settings.update({
        appsDomain: form.appsDomain.trim(),
        publicUrl: form.publicUrl.trim(),
        network: form.network.trim(),
        traefikDynamicDir: form.traefikDynamicDir.trim(),
        entryPoints: form.entryPoints.split(/[\s,]+/).filter(Boolean),
        certResolver: form.certResolver.trim(),
        keepImages: Number(form.keepImages),
        keepDeployments: Number(form.keepDeployments),
        buildConcurrency: Number(form.buildConcurrency),
      });
      notify.success(t('server.saved'));
      load();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

{#if system}
  <Card title={t('server.status')}>
    <div class="status">
      <div class="item">
        <span class="small muted">{t('server.docker')}</span>
        {#if system.docker?.ok}
          <Badge tone="good" dot>{system.docker.version}</Badge>
        {:else}
          <Badge tone="bad" dot>{t('server.unreachable')}</Badge>
        {/if}
        {#if system.docker?.error}<p class="small err">{system.docker.error}</p>{/if}
      </div>

      <div class="item">
        <span class="small muted">{t('server.traefikConfig')}</span>
        {#if system.traefik?.ok}
          <Badge tone="good" dot>{t('server.writable')}</Badge>
        {:else}
          <Badge tone="bad" dot>{t('server.notWritable')}</Badge>
        {/if}
        <code class="small truncate">{system.traefik?.file}</code>
        {#if system.traefik?.error}<p class="small err">{system.traefik.error}</p>{/if}
      </div>

      <div class="item">
        <span class="small muted">{t('server.network')}</span>
        {#if system.network?.exists}
          <Badge tone="good" dot>{system.network.name}</Badge>
        {:else}
          <Badge tone="warn" dot>{t('server.networkPending')}</Badge>
        {/if}
      </div>

      <div class="item">
        <span class="small muted">{t('server.projects')}</span>
        <strong>{t('server.liveCount', { count: system.projects?.live ?? 0 })}</strong>
        <span class="small faint">{t('server.ofTotal', { count: system.projects?.total ?? 0 })}</span>
      </div>
    </div>
  </Card>
{/if}

{#if settings}
  <Card title={t('server.addresses')}>
    <div class="form">
      <Field label={t('server.appsDomain')} hint={t('server.appsDomainHint')}>
        {#snippet children(id)}
          <input {id} bind:value={form.appsDomain} placeholder="apps.example.com" autocomplete="off" />
        {/snippet}
      </Field>

      <Field label={t('server.publicUrl')} hint={t('server.publicUrlHint')}>
        {#snippet children(id)}
          <input {id} bind:value={form.publicUrl} placeholder="https://publix.example.com" autocomplete="off" />
        {/snippet}
      </Field>
    </div>
  </Card>

  <Card title={t('server.traefik')}>
    <div class="form">
      <Field label={t('server.dynamicDir')} hint={t('server.dynamicDirHint')}>
        {#snippet children(id)}
          <input {id} bind:value={form.traefikDynamicDir} autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <div class="two">
        <Field label={t('server.entrypoints')} hint={t('server.entrypointsHint')}>
          {#snippet children(id)}<input {id} bind:value={form.entryPoints} autocomplete="off" />{/snippet}
        </Field>
        <Field label={t('server.certResolver')} hint={t('server.certResolverHint')}>
          {#snippet children(id)}<input {id} bind:value={form.certResolver} autocomplete="off" />{/snippet}
        </Field>
      </div>

      <Field label={t('server.dockerNetwork')} hint={t('server.dockerNetworkHint')}>
        {#snippet children(id)}<input {id} bind:value={form.network} autocomplete="off" />{/snippet}
      </Field>
    </div>
  </Card>

  <Card title={t('server.retention')}>
    <div class="three">
      <Field label={t('server.keepImages')} hint={t('server.keepImagesHint')}>
        {#snippet children(id)}<input {id} type="number" min="1" max="20" bind:value={form.keepImages} />{/snippet}
      </Field>

      <Field label={t('server.keepDeployments')} hint={t('server.keepDeploymentsHint')}>
        {#snippet children(id)}
          <input {id} type="number" min="1" max="500" bind:value={form.keepDeployments} />
        {/snippet}
      </Field>

      <Field label={t('server.buildConcurrency')} hint={t('server.buildConcurrencyHint')}>
        {#snippet children(id)}
          <input {id} type="number" min="1" max="16" bind:value={form.buildConcurrency} />
        {/snippet}
      </Field>
    </div>

    <p class="muted small note">{t('server.retentionNote')}</p>
  </Card>

  <div class="save">
    <Button variant="primary" pending={saving} onclick={save}>{t('server.save')}</Button>
    <Button variant="ghost" onclick={load}>{t('common.reload')}</Button>
  </div>
{/if}

<style>
  .status {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 14px;
  }
  .item { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; min-width: 0; }
  .item code { color: var(--text-muted); max-width: 100%; }
  .err { color: var(--bad); margin: 0; }

  .form { display: flex; flex-direction: column; gap: 14px; }
  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
  .three { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 14px; }
  @media (max-width: 620px) { .two { grid-template-columns: 1fr; } }

  .note { margin-top: 14px; max-width: 78ch; line-height: 1.55; }

  .save { display: flex; gap: 8px; margin-top: 16px; }

  :global(.card + .card) { margin-top: 14px; }
</style>
