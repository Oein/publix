<script>
  import { api } from '../lib/api.js';
  import { notify } from '../lib/toast.js';
  import Modal from '../lib/Modal.svelte';
  import Button from '../lib/Button.svelte';
  import Field from '../lib/Field.svelte';
  import Badge from '../lib/Badge.svelte';
  import FrameworkIcon from '../lib/FrameworkIcon.svelte';
  import { t, tparts } from '../lib/i18n.svelte.js';

  /**
   * The import dialog.
   *
   * It inspects the repository first and shows what publix worked out, so
   * the user confirms a decision they can see rather than pressing a button
   * and hoping. Everything on this screen is editable before committing.
   */
  let { repo, onclose, onimported } = $props();

  let inspection = $state(null);
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
  let domains = $state('');
  let autoDeploy = $state(true);
  let deployNow = $state(true);
  let writeSpec = $state(false);
  let spec = $state('');
  let showSpec = $state(false);

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

  async function submit() {
    importing = true;
    try {
      const result = await api.github.import({
        owner: repo.owner,
        repo: repo.name,
        branch,
        name: name.trim(),
        rootDir: rootDir.trim(),
        domains: domains
          .split(/[\s,]+/)
          .map((d) => d.trim())
          .filter(Boolean),
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
    <div class="detected">
      <div class="row">
        <FrameworkIcon
          framework={inspection.detection.framework || inspection.detection.kind}
          title={inspection.detection.name}
          size={22}
        />
        <div class="col" style="gap:1px">
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
          <span class="grow"></span>
          <Badge tone="muted">{inspection.detection.configFile}</Badge>
        {/if}
      </div>

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

      {#each inspection.warnings ?? [] as warning}
        <div class="notice warn"><p class="small">{warning}</p></div>
      {/each}
    </div>
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

    <div class="two">
      <Field label={t('id.rootDir')} hint={t('id.rootDirHint')}>
        {#snippet children(id)}
          <input {id} bind:value={rootDir} placeholder="apps/web" autocomplete="off" />
        {/snippet}
      </Field>

      <Field label={t('id.domains')} hint={t('id.domainsHint')}>
        {#snippet children(id)}
          <input {id} bind:value={domains} placeholder="app.example.com" autocomplete="off" />
        {/snippet}
      </Field>
    </div>

    <label class="check">
      <input type="checkbox" bind:checked={autoDeploy} />
      <span>
        <strong>{t('id.autoDeploy')}</strong>
        <span class="small muted">{t('id.autoDeployHint')}</span>
      </span>
    </label>

    <label class="check">
      <input type="checkbox" bind:checked={deployNow} />
      <span>
        <strong>{t('id.deployNow')}</strong>
        <span class="small muted">{t('id.deployNowHint')}</span>
      </span>
    </label>

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
        <button class="disclose" onclick={() => (showSpec = !showSpec)}>
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
    padding: 12px 14px;
    margin-bottom: 16px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  dl {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 6px 18px;
    margin: 10px 0 0;
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

  .form { display: flex; flex-direction: column; gap: 14px; }

  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }
  @media (max-width: 620px) { .two { grid-template-columns: 1fr; } }

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
