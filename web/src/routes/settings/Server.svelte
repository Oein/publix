<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import Badge from '../../lib/Badge.svelte';

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
      notify.success('Settings saved — routing was rewritten.');
      load();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

{#if system}
  <Card title="Status">
    <div class="status">
      <div class="item">
        <span class="small muted">Docker</span>
        {#if system.docker?.ok}
          <Badge tone="good" dot>{system.docker.version}</Badge>
        {:else}
          <Badge tone="bad" dot>unreachable</Badge>
        {/if}
        {#if system.docker?.error}<p class="small err">{system.docker.error}</p>{/if}
      </div>

      <div class="item">
        <span class="small muted">Traefik config</span>
        {#if system.traefik?.ok}
          <Badge tone="good" dot>writable</Badge>
        {:else}
          <Badge tone="bad" dot>not writable</Badge>
        {/if}
        <code class="small truncate">{system.traefik?.file}</code>
        {#if system.traefik?.error}<p class="small err">{system.traefik.error}</p>{/if}
      </div>

      <div class="item">
        <span class="small muted">Network</span>
        {#if system.network?.exists}
          <Badge tone="good" dot>{system.network.name}</Badge>
        {:else}
          <Badge tone="warn" dot>created on first deploy</Badge>
        {/if}
      </div>

      <div class="item">
        <span class="small muted">Projects</span>
        <strong>{system.projects?.live ?? 0} live</strong>
        <span class="small faint">of {system.projects?.total ?? 0}</span>
      </div>
    </div>
  </Card>
{/if}

{#if settings}
  <Card title="Addresses">
    <div class="form">
      <Field
        label="Apps domain"
        hint="A wildcard domain, so every project gets a working URL before you configure one. With apps.example.com a project named blog is reachable at blog.apps.example.com. Point *.apps.example.com at this host."
      >
        {#snippet children(id)}
          <input {id} bind:value={form.appsDomain} placeholder="apps.example.com" autocomplete="off" />
        {/snippet}
      </Field>

      <Field
        label="Public URL"
        hint="Where this dashboard is reachable from the internet. GitHub webhooks are pointed here, so deploy-on-push needs it."
      >
        {#snippet children(id)}
          <input {id} bind:value={form.publicUrl} placeholder="https://publix.example.com" autocomplete="off" />
        {/snippet}
      </Field>
    </div>
  </Card>

  <Card title="Traefik">
    <div class="form">
      <Field
        label="Dynamic configuration directory"
        hint="Traefik's file provider directory. publix owns exactly one file in it and leaves everything else alone."
      >
        {#snippet children(id)}
          <input {id} bind:value={form.traefikDynamicDir} autocomplete="off" spellcheck="false" />
        {/snippet}
      </Field>

      <div class="two">
        <Field label="Entrypoints" hint="Comma separated.">
          {#snippet children(id)}<input {id} bind:value={form.entryPoints} autocomplete="off" />{/snippet}
        </Field>
        <Field label="Certificate resolver" hint="Your ACME resolver name. Empty disables TLS.">
          {#snippet children(id)}<input {id} bind:value={form.certResolver} autocomplete="off" />{/snippet}
        </Field>
      </div>

      <Field label="Docker network" hint="Shared by Traefik and every project container.">
        {#snippet children(id)}<input {id} bind:value={form.network} autocomplete="off" />{/snippet}
      </Field>
    </div>
  </Card>

  <Card title="Retention and builds">
    <div class="three">
      <Field
        label="Images per project"
        hint="Two keeps a one-step rollback instant. Older deployments rebuild from their commit."
      >
        {#snippet children(id)}<input {id} type="number" min="1" max="20" bind:value={form.keepImages} />{/snippet}
      </Field>

      <Field label="Deployment records" hint="History depth. Metadata only — cheap to keep.">
        {#snippet children(id)}
          <input {id} type="number" min="1" max="500" bind:value={form.keepDeployments} />
        {/snippet}
      </Field>

      <Field label="Concurrent builds" hint="Caps how many projects build at once.">
        {#snippet children(id)}
          <input {id} type="number" min="1" max="16" bind:value={form.buildConcurrency} />
        {/snippet}
      </Field>
    </div>

    <p class="muted small note">
      Only one deployment per project stays running at a time. During a deploy the new generation
      briefly runs alongside the old one so traffic can move without dropping a request, then the
      old one is stopped.
    </p>
  </Card>

  <div class="save">
    <Button variant="primary" pending={saving} onclick={save}>Save settings</Button>
    <Button variant="ghost" onclick={load}>Reload</Button>
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
