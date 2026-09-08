<script>
  import { api } from '../lib/api.js';
  import { notify } from '../lib/toast.js';
  import Modal from '../lib/Modal.svelte';
  import Button from '../lib/Button.svelte';
  import Field from '../lib/Field.svelte';
  import Badge from '../lib/Badge.svelte';
  import Disclosure from '../lib/Disclosure.svelte';
  import EnvRows from '../lib/EnvRows.svelte';
  import FrameworkIcon from '../lib/FrameworkIcon.svelte';
  import { t, tparts } from '../lib/i18n.svelte.js';

  /**
   * The import dialog.
   *
   * Two decisions matter here and everything else has a good default: what
   * this project is called, and what address it answers on. Those are the
   * only fields open on arrival. The rest — custom domains, environment,
   * the build itself — sit behind sections that say what they contain, so
   * the screen is short without hiding anything.
   *
   * What publix worked out about the repository is shown rather than
   * asked, because confirming something you can see beats pressing a
   * button and hoping.
   */
  let { repo, onclose, onimported } = $props();

  let inspection = $state(null);
  // The parent domains registered on this server, so importing is where
  // someone picks where the thing will actually live.
  let appsDomains = $state([]);
  let inspectError = $state('');
  let importing = $state(false);

  // Seeding state from a prop is intentional here: these are editable
  // defaults, and the dialog is keyed by repository so a different repo
  // mounts a fresh copy rather than reusing this one.
  /* svelte-ignore state_referenced_locally */
  let name = $state(repo.name);
  /* svelte-ignore state_referenced_locally */
  let branch = $state(repo.default_branch || 'main');
  let rootDir = $state('');
  let appsDomain = $state('');
  let domains = $state('');
  let env = $state([]);
  let autoDeploy = $state(true);
  let deployNow = $state(true);
  let writeSpec = $state(false);
  let spec = $state('');
  let showSpec = $state(false);
  let showDetails = $state(false);

  // The subdomain follows the name until someone types one, at which point
  // it is theirs: a URL people may already have been told should not move
  // because the display name was tidied up afterwards.
  let subdomain = $state(slugify(repo.name));
  let subdomainTouched = $state(false);

  function slugify(value) {
    return value
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 40)
      .replace(/-+$/, '');
  }

  async function inspect() {
    inspection = null;
    inspectError = '';
    try {
      inspection = await api.github.inspect(repo.owner, repo.name, branch);
      spec = inspection.hasSpec ? inspection.spec : inspection.suggested;
      // Offering to commit a file into someone's repository should be an
      // opt-in, but it is the right default when the repo has no spec:
      // it makes the next deploy reproducible from the repository alone.
      writeSpec = !inspection.hasSpec && Boolean(inspection.suggested);
    } catch (err) {
      inspectError = err.message;
    }
  }

  $effect(() => {
    branch;
    inspect();
  });

  $effect(() => {
    api.settings
      .get()
      .then((s) => (appsDomains = s.appsDomains ?? []))
      .catch(() => (appsDomains = []));
  });

  $effect(() => {
    const derived = slugify(name);
    if (!subdomainTouched) subdomain = derived;
  });

  const defaultAppsDomain = $derived(
    appsDomains.find((d) => d.default)?.domain ?? appsDomains[0]?.domain ?? ''
  );
  const parentDomain = $derived(
    appsDomain === 'none' ? '' : appsDomain || defaultAppsDomain
  );
  // What the project's address will be, resolved the same way the server
  // resolves it, so the dialog is not promising something different.
  const generatedHost = $derived(
    parentDomain ? `${subdomain || 'project'}.${parentDomain}` : ''
  );

  const customDomains = $derived(
    domains
      .split(/[\s,]+/)
      .map((d) => d.trim())
      .filter(Boolean)
  );
  const namedEnv = $derived(env.filter((e) => e.key.trim()));

  // Every address this project will answer on, which is the one thing
  // worth being sure about before pressing the button.
  const addresses = $derived([generatedHost, ...customDomains].filter(Boolean));

  async function submit() {
    importing = true;
    try {
      const result = await api.github.import({
        owner: repo.owner,
        repo: repo.name,
        branch,
        name: name.trim(),
        slug: subdomain.trim(),
        rootDir: rootDir.trim(),
        domains: customDomains,
        appsDomain,
        env: namedEnv.map((e) => ({ key: e.key.trim(), value: e.value, secret: e.secret })),
        autoDeploy,
        deploy: deployNow,
        writeSpec: writeSpec && !inspection?.hasSpec,
        spec,
      });
      onimported(result);
    } catch (err) {
      notify.error(err);
    } finally {
      importing = false;
    }
  }
</script>

<Modal title={t('id.title', { repo: repo.full_name })} wide {onclose}>
  {#if inspectError}
    <div class="notice bad">
      <strong>{t('id.inspectFailed')}</strong>
      <p class="small muted">{inspectError}</p>
    </div>
  {:else if inspection === null}
    <div class="inspecting"><span class="spinner"></span> {t('id.inspecting')}</div>
  {:else}
    <!-- The detected build is information, not a question. It stays one
         line unless someone wants the details behind it. -->
    <button
      type="button"
      class="detected"
      aria-expanded={showDetails}
      onclick={() => (showDetails = !showDetails)}
    >
      <FrameworkIcon
        framework={inspection.detection.framework || inspection.detection.kind}
        title={inspection.detection.name}
        size={22}
      />
      <div class="col">
        <strong class="small">{inspection.detection.name || inspection.detection.kind}</strong>
        {#if inspection.hasSpec}
          <span class="small muted">
            {#each tparts('id.usingSpec') as part}{#if part.slot}<code>{inspection.specPath}</code
              >{:else}{part.text}{/if}{/each}
          </span>
        {:else if inspection.detection.generated}
          <span class="small muted">{t('id.generated')}</span>
        {:else}
          <span class="small muted">{t('id.detected')}</span>
        {/if}
      </div>
      {#if inspection.detection.configFile}
        <Badge tone="muted">{inspection.detection.configFile}</Badge>
      {/if}
      <span class="caret" aria-hidden="true">{showDetails ? '▾' : '▸'}</span>
    </button>

    {#if showDetails}
      <dl>
        <div><dt>{t('id.build')}</dt><dd>{inspection.detection.kind}</dd></div>
        {#if inspection.detection.compose}
          <div>
            <dt>{t('id.compose')}</dt><dd><code>{inspection.detection.compose}</code></dd>
          </div>
        {/if}
        {#if inspection.detection.dockerfile}
          <div>
            <dt>{t('id.dockerfile')}</dt><dd><code>{inspection.detection.dockerfile}</code></dd>
          </div>
        {/if}
        {#if inspection.detection.install}
          <div>
            <dt>{t('id.install')}</dt><dd><code>{inspection.detection.install}</code></dd>
          </div>
        {/if}
        {#if inspection.detection.command}
          <div>
            <dt>{t('id.build')}</dt><dd><code>{inspection.detection.command}</code></dd>
          </div>
        {/if}
        {#if inspection.detection.start}
          <div><dt>{t('id.start')}</dt><dd><code>{inspection.detection.start}</code></dd></div>
        {/if}
        {#if inspection.detection.standalone}
          <div><dt>{t('id.output')}</dt><dd>{t('id.standalone')}</dd></div>
        {/if}
        {#if inspection.detection.output}
          <div>
            <dt>{t('id.output')}</dt><dd><code>{inspection.detection.output}</code></dd>
          </div>
        {/if}
        {#if inspection.detection.port}
          <div><dt>{t('id.port')}</dt><dd>{inspection.detection.port}</dd></div>
        {/if}
      </dl>
    {/if}

    {#each inspection.warnings ?? [] as warning}
      <div class="notice warn"><p class="small">{warning}</p></div>
    {/each}
  {/if}

  <div class="form">
    <div class="two">
      <Field label={t('id.name')} required>
        {#snippet children(id)}<input {id} bind:value={name} autocomplete="off" />{/snippet}
      </Field>

      <Field label={t('id.branch')} hint={t('id.branchHint')}>
        {#snippet children(id)}
          <select {id} bind:value={branch}>
            {#each inspection?.branches?.length ? inspection.branches : [branch] as b}
              <option value={b}>{b}</option>
            {/each}
          </select>
        {/snippet}
      </Field>
    </div>

    <!-- The address, built the way it reads: a name you choose, a dot, and
         a parent domain you pick. Typing a whole hostname into a box and
         hoping publix agrees with it was the old way. -->
    {#if appsDomains.length > 0}
      <Field label={t('id.address')} hint={t('id.addressHint')}>
        {#snippet children(id)}
          <div class="host" class:off={!parentDomain}>
            <input
              {id}
              class="sub"
              value={subdomain}
              disabled={!parentDomain}
              oninput={(e) => {
                subdomainTouched = true;
                subdomain = slugify(e.currentTarget.value);
                e.currentTarget.value = subdomain;
              }}
              placeholder="project"
              autocomplete="off"
              spellcheck="false"
            />
            <span class="dot" aria-hidden="true">.</span>
            <select bind:value={appsDomain} aria-label={t('id.appsDomain')}>
              <option value="">{defaultAppsDomain}</option>
              {#each appsDomains as d}
                {#if d.domain !== defaultAppsDomain}
                  <option value={d.domain}>{d.domain}</option>
                {/if}
              {/each}
              <option value="none">{t('id.appsDomainNone')}</option>
            </select>
          </div>
        {/snippet}
      </Field>
      <p class="address small">
        {#if generatedHost}
          {t('id.willBeAt')} <code>https://{generatedHost}</code>
        {:else if customDomains.length > 0}
          {t('id.willBeAt')} <code>https://{customDomains[0]}</code>
        {:else}
          {t('id.noGeneratedHost')}
        {/if}
      </p>
    {/if}

    <Disclosure
      label={t('id.customDomains')}
      summary={customDomains.length ? customDomains.join(', ') : t('id.domainsNone')}
    >
      <Field label={t('id.domains')} hint={t('id.domainsHint')}>
        {#snippet children(id)}
          <input {id} bind:value={domains} placeholder="app.example.com" autocomplete="off" />
        {/snippet}
      </Field>
    </Disclosure>

    <Disclosure
      label={t('env.title')}
      summary={namedEnv.length
        ? t('id.envCount', { count: namedEnv.length })
        : t('id.envNone')}
    >
      <p class="small muted nomargin">{t('id.envBlurb')}</p>
      <EnvRows bind:rows={env} />
    </Disclosure>

    <Disclosure label={t('id.advanced')} summary={rootDir || t('id.advancedNone')}>
      <Field label={t('id.rootDir')} hint={t('id.rootDirHint')}>
        {#snippet children(id)}
          <input {id} bind:value={rootDir} placeholder="apps/web" autocomplete="off" />
        {/snippet}
      </Field>

      {#if inspection && !inspection.hasSpec && inspection.suggested}
        <label class="check">
          <input type="checkbox" bind:checked={writeSpec} />
          <span>
            <strong>{t('id.writeSpec')}</strong>
            <span class="small muted">{t('id.writeSpecHint')}</span>
          </span>
        </label>
      {/if}

      {#if spec}
        <div class="specbox">
          <button type="button" class="disclose" onclick={() => (showSpec = !showSpec)}>
            {showSpec ? '▾' : '▸'}
            {inspection?.hasSpec ? t('id.repoSpec') : t('id.suggestedSpec')}
          </button>
          {#if showSpec}
            {#if inspection?.hasSpec}
              <pre class="mono">{spec}</pre>
            {:else}
              <textarea bind:value={spec} spellcheck="false" rows="12"></textarea>
            {/if}
          {/if}
        </div>
      {/if}
    </Disclosure>

    <div class="checks">
      <label class="check">
        <input type="checkbox" bind:checked={deployNow} />
        <span>
          <strong>{t('id.deployNow')}</strong>
          <span class="small muted">{t('id.deployNowHint')}</span>
        </span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={autoDeploy} />
        <span>
          <strong>{t('id.autoDeploy')}</strong>
          <span class="small muted">{t('id.autoDeployHint')}</span>
        </span>
      </label>
    </div>

    {#if addresses.length === 0}
      <p class="notice warn small">{t('id.noAddressWarning')}</p>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={onclose}>{t('common.cancel')}</Button>
    <Button
      variant="primary"
      pending={importing}
      disabled={!name.trim() || inspection === null}
      onclick={submit}
    >
      {deployNow ? t('id.importAndDeploy') : t('id.import')}
    </Button>
  {/snippet}
</Modal>

<style>
  .inspecting {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 14px 0;
    color: var(--text-muted);
    font-size: 13px;
  }

  .spinner {
    width: 14px;
    height: 14px;
    border: 2px solid var(--border-strong);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .detected {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 10px 12px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    cursor: pointer;
    text-align: left;
    color: var(--text);
    font: inherit;
  }
  .detected:hover { background: var(--bg-hover); }
  .detected .col { display: flex; flex-direction: column; gap: 1px; margin-right: auto; }
  .caret { color: var(--text-muted); font-size: 10px; }

  dl {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 6px 18px;
    margin: 10px 0 0;
    padding: 0 2px;
  }
  dl div { display: flex; gap: 8px; font-size: 12.5px; }
  dt { color: var(--text-muted); min-width: 92px; }
  dd { margin: 0; }

  .notice {
    padding: 8px 10px;
    margin-top: 10px;
    border-radius: var(--radius-sm);
    border-left: 3px solid;
  }
  .notice p { margin: 0; }
  .warn { background: var(--warn-bg); border-color: var(--warn); }
  .bad { background: var(--bad-bg); border-color: var(--bad); }

  .form { display: flex; flex-direction: column; gap: 14px; margin-top: 16px; }
  .nomargin { margin: 0; }

  /* Sits directly under the picker it explains, so it must not inherit the
     form's gap and float away from it. */
  .address { margin: -8px 0 0; color: var(--text-muted); }

  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }
  @media (max-width: 620px) { .two { grid-template-columns: 1fr; } }

  .host { display: flex; align-items: center; gap: 6px; }
  .host .sub { flex: 1 1 45%; min-width: 0; font-family: var(--mono); font-size: 12.5px; }
  .host select { flex: 1 1 55%; min-width: 0; }
  .host .dot { color: var(--text-muted); flex: none; }
  .host.off .sub { opacity: 0.5; }

  .checks { display: flex; flex-direction: column; gap: 10px; }

  .check {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    cursor: pointer;
  }
  .check input { margin-top: 2px; flex: none; }
  .check span { display: flex; flex-direction: column; gap: 1px; }
  .check strong { font-size: 13px; font-weight: 550; }

  .specbox {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .disclose {
    width: 100%;
    text-align: left;
    padding: 8px 11px;
    background: var(--bg-sunken);
    border: none;
    font-size: 12.5px;
    font-weight: 550;
    cursor: pointer;
  }
  .disclose:hover { background: var(--bg-hover); }

  pre, textarea {
    margin: 0;
    width: 100%;
    padding: 11px;
    background: var(--bg-raised);
    border: none;
    border-top: 1px solid var(--border);
    font-family: var(--mono);
    font-size: 12.5px;
    line-height: 1.55;
    white-space: pre-wrap;
    resize: vertical;
  }
  textarea:focus { outline: none; box-shadow: inset 0 0 0 2px var(--accent-bg); }

  code {
    padding: 0 4px;
    background: var(--bg-raised);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }
</style>
