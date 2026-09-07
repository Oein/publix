<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import { t } from '../../lib/i18n.svelte.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import Badge from '../../lib/Badge.svelte';
  import Empty from '../../lib/Empty.svelte';
  import Modal from '../../lib/Modal.svelte';
  import Confirm from '../../lib/Confirm.svelte';

  /**
   * Server-wide domains.
   *
   * Two things live here because they are the same decision from opposite
   * ends: which parent domains projects can sit under, and what happens to
   * hostnames with nothing sitting under them at all.
   */
  let { revision } = $props();

  let settings = $state(null);

  // Apps domains.
  let addingParent = $state(false);
  let parentDomain = $state('');
  let parentDescription = $state('');
  let removingParent = $state(null);

  // Proxy rules. `editing` holds the rule's original domain, which is its
  // key, so a rename edits in place rather than creating a second one.
  let editingProxy = $state(null);
  let pxDomain = $state('');
  let pxPath = $state('');
  let pxTarget = $state('');
  let pxStripPath = $state(false);
  let pxPassHost = $state(true);
  let pxInsecure = $state(false);
  let pxDescription = $state('');
  let removingProxy = $state(null);

  // Forwarding rules. `editing` holds the rule's original domain, which is
  // its key, so a rename edits in place rather than creating a second one.
  let editingRule = $state(null);
  let ruleDomain = $state('');
  let rulePath = $state('');
  let ruleTarget = $state('');
  let ruleKeepPath = $state(true);
  let rulePermanent = $state(false);
  let ruleDescription = $state('');
  let removingRule = $state(null);
  let saving = $state(false);
  // A refusal belongs next to the field that caused it, not in a toast in
  // the corner while the form is still open.
  let formError = $state(null);

  function failed(err) {
    formError = { message: err.message, details: err.details ?? [] };
  }

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

  function openAddParent() {
    parentDomain = '';
    parentDescription = '';
    formError = null;
    addingParent = true;
  }

  async function addParent() {
    saving = true;
    formError = null;
    try {
      settings = await api.settings.addAppsDomain({
        domain: parentDomain.trim(),
        description: parentDescription.trim(),
      });
      notify.success(t('domainsPage.registered', { domain: parentDomain.trim() }));
      addingParent = false;
    } catch (err) {
      failed(err);
    } finally {
      saving = false;
    }
  }

  async function makeDefault(domain) {
    try {
      settings = await api.settings.defaultAppsDomain(domain);
      notify.success(t('domainsPage.defaultSet', { domain }));
    } catch (err) {
      notify.error(err);
    }
  }

  async function removeParent() {
    try {
      settings = await api.settings.removeAppsDomain(removingParent.domain);
      notify.success(t('domainsPage.unregistered', { domain: removingParent.domain }));
      removingParent = null;
    } catch (err) {
      notify.error(err);
    }
  }

  function openProxy(rule) {
    editingProxy = rule ?? { isNew: true };
    pxDomain = rule?.domain ?? '';
    pxPath = rule?.path ?? '';
    pxTarget = rule?.target ?? '';
    pxStripPath = rule?.stripPath ?? false;
    pxPassHost = rule ? rule.passHostHeader !== false : true;
    pxInsecure = rule?.insecureSkipVerify ?? false;
    pxDescription = rule?.description ?? '';
    formError = null;
  }

  async function saveProxy() {
    const body = {
      domain: pxDomain.trim(),
      path: pxPath.trim(),
      target: pxTarget.trim(),
      stripPath: pxStripPath,
      passHostHeader: pxPassHost,
      insecureSkipVerify: pxInsecure,
      description: pxDescription.trim(),
    };
    saving = true;
    formError = null;
    try {
      settings = editingProxy.isNew
        ? await api.settings.addProxy(body)
        : await api.settings.updateProxy(editingProxy.domain, body);
      notify.success(t('domainsPage.proxySaved', { domain: body.domain }));
      editingProxy = null;
    } catch (err) {
      failed(err);
    } finally {
      saving = false;
    }
  }

  async function removeProxy() {
    try {
      settings = await api.settings.removeProxy(removingProxy.domain);
      notify.success(t('domainsPage.proxyRemoved', { domain: removingProxy.domain }));
      removingProxy = null;
    } catch (err) {
      notify.error(err);
    }
  }

  function openRule(rule) {
    editingRule = rule ?? { isNew: true };
    ruleDomain = rule?.domain ?? '';
    rulePath = rule?.path ?? '';
    ruleTarget = rule?.target ?? '';
    ruleKeepPath = rule ? rule.keepPath !== false : true;
    rulePermanent = rule?.permanent ?? false;
    ruleDescription = rule?.description ?? '';
    formError = null;
  }

  async function saveRule() {
    const body = {
      domain: ruleDomain.trim(),
      path: rulePath.trim(),
      target: ruleTarget.trim(),
      keepPath: ruleKeepPath,
      permanent: rulePermanent,
      description: ruleDescription.trim(),
    };
    saving = true;
    formError = null;
    try {
      settings = editingRule.isNew
        ? await api.settings.addRedirect(body)
        : await api.settings.updateRedirect(editingRule.domain, body);
      notify.success(t('domainsPage.ruleSaved', { domain: body.domain }));
      editingRule = null;
    } catch (err) {
      failed(err);
    } finally {
      saving = false;
    }
  }

  async function removeRule() {
    try {
      settings = await api.settings.removeRedirect(removingRule.domain);
      notify.success(t('domainsPage.ruleRemoved', { domain: removingRule.domain }));
      removingRule = null;
    } catch (err) {
      notify.error(err);
    }
  }

  // What the rule under construction will actually do, spelled out. A
  // redirect is the kind of thing people get backwards, and reading it
  // back in full is cheaper than finding out from a browser loop.
  // What the backend URL actually becomes, since a bare host:port gains a
  // scheme and a stripped prefix changes what the backend sees.
  const proxyPreview = $derived.by(() => {
    const to = pxTarget.trim() || '10.0.0.5:3000';
    const base = /^https?:\/\//.test(to) ? to : `http://${to}`;
    const tail = pxPath.trim() && !pxStripPath ? pxPath.trim() : '';
    return base.replace(/\/$/, '') + tail;
  });

  const rulePreview = $derived.by(() => {
    const from = ruleDomain.trim() || 'old.example.com';
    const to = ruleTarget.trim() || 'new.example.com';
    const target = /^https?:\/\//.test(to) ? to : `https://${to}`;
    const path = ruleKeepPath ? '/some/page' : '';
    return {
      from: `https://${from}${rulePath.trim()}${ruleKeepPath ? '/some/page' : ''}`,
      to: `${target.replace(/\/$/, '')}${path}` || target,
    };
  });
</script>

<Card title={t('domainsPage.appsTitle')} description={t('domainsPage.appsDesc')} flush>
  {#snippet actions()}
    <Button size="sm" variant="primary" onclick={openAddParent}>
      {t('domainsPage.addDomain')}
    </Button>
  {/snippet}

  {#if settings === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if settings.appsDomains.length === 0}
    <Empty title={t('domainsPage.noAppsTitle')} description={t('domainsPage.noAppsDesc')}>
      <Button variant="primary" onclick={openAddParent}>{t('domainsPage.addDomain')}</Button>
    </Empty>
  {:else}
    <div class="table-scroll">
      <table class="table">
        <thead>
          <tr>
            <th>{t('domainsPage.domain')}</th>
            <th>{t('domainsPage.colAddress')}</th>
            <th>{t('domainsPage.colProjects')}</th>
            <th class="actions"></th>
          </tr>
        </thead>
        <tbody>
          {#each settings.appsDomains as d (d.domain)}
            <tr>
              <td>
                <div class="cell">
                  <code>{d.domain}</code>
                  {#if d.default}<Badge tone="good">{t('domainsPage.default')}</Badge>{/if}
                </div>
                {#if d.description}<div class="sub">{d.description}</div>{/if}
              </td>
              <td class="faint mono">{d.example}</td>
              <td>
                {#if d.usedBy.length}
                  <span class="truncate" title={d.usedBy.join(', ')}>{d.usedBy.join(', ')}</span>
                {:else}
                  <span class="faint">—</span>
                {/if}
              </td>
              <td class="actions">
                {#if !d.default}
                  <Button size="sm" variant="ghost" onclick={() => makeDefault(d.domain)}>
                    {t('domainsPage.makeDefault')}
                  </Button>
                {/if}
                <Button size="sm" variant="danger" onclick={() => (removingParent = d)}>
                  {t('domainsPage.unregister')}
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>

<Card title={t('domainsPage.proxyTitle')} description={t('domainsPage.proxyDesc')} flush>
  {#snippet actions()}
    <Button size="sm" onclick={() => openProxy(null)}>{t('domainsPage.addProxy')}</Button>
  {/snippet}

  {#if settings === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if settings.proxies.length === 0}
    <Empty title={t('domainsPage.noProxiesTitle')} description={t('domainsPage.noProxiesDesc')}>
      <Button onclick={() => openProxy(null)}>{t('domainsPage.addProxy')}</Button>
    </Empty>
  {:else}
    <div class="table-scroll">
      <table class="table">
        <thead>
          <tr>
            <th>{t('domainsPage.domain')}</th>
            <th>{t('domainsPage.backend')}</th>
            <th>{t('domainsPage.colBehaviour')}</th>
            <th class="actions"></th>
          </tr>
        </thead>
        <tbody>
          {#each settings.proxies as rule (rule.domain + rule.path)}
            <tr>
              <td>
                <code>{rule.domain}{rule.path}</code>
                {#if rule.description}<div class="sub">{rule.description}</div>{/if}
              </td>
              <td><code class="target">{rule.resolvedTarget}</code></td>
              <td>
                <div class="cell">
                  {#if rule.stripPath}<Badge tone="muted">{t('domainsPage.stripped')}</Badge>{/if}
                  {#if rule.passHostHeader === false}
                    <Badge tone="muted">{t('domainsPage.ownHost')}</Badge>
                  {/if}
                  {#if rule.insecureSkipVerify}
                    <Badge tone="warn">{t('domainsPage.insecure')}</Badge>
                  {/if}
                  {#if rule.shadowedBy}
                    <Badge tone="bad">{t('domainsPage.shadowed', { project: rule.shadowedBy })}</Badge>
                  {/if}
                </div>
              </td>
              <td class="actions">
                <Button size="sm" variant="ghost" onclick={() => openProxy(rule)}>
                  {t('domainsPage.edit')}
                </Button>
                <Button size="sm" variant="danger" onclick={() => (removingProxy = rule)}>
                  {t('common.delete')}
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>

<Card title={t('domainsPage.forwardTitle')} description={t('domainsPage.forwardDesc')} flush>
  {#snippet actions()}
    <Button size="sm" onclick={() => openRule(null)}>{t('domainsPage.addRule')}</Button>
  {/snippet}

  {#if settings === null}
    <p class="pad muted small">{t('common.loading')}</p>
  {:else if settings.redirects.length === 0}
    <Empty title={t('domainsPage.noRulesTitle')} description={t('domainsPage.noRulesDesc')}>
      <Button onclick={() => openRule(null)}>{t('domainsPage.addRule')}</Button>
    </Empty>
  {:else}
    <div class="table-scroll">
      <table class="table">
        <thead>
          <tr>
            <th>{t('domainsPage.from')}</th>
            <th>{t('domainsPage.to')}</th>
            <th>{t('domainsPage.colBehaviour')}</th>
            <th class="actions"></th>
          </tr>
        </thead>
        <tbody>
          {#each settings.redirects as rule (rule.domain + rule.path)}
            <tr>
              <td>
                <code>{rule.domain}{rule.path}</code>
                {#if rule.description}<div class="sub">{rule.description}</div>{/if}
              </td>
              <td><code class="target">{rule.resolvedTarget}</code></td>
              <td>
                <div class="cell">
                  <Badge tone={rule.permanent ? 'warn' : 'muted'}>
                    {rule.permanent ? t('domainsPage.permanent') : t('domainsPage.temporary')}
                  </Badge>
                  {#if rule.keepPath === false}
                    <Badge tone="muted">{t('domainsPage.dropPath')}</Badge>
                  {/if}
                  {#if rule.shadowedBy}
                    <Badge tone="bad">{t('domainsPage.shadowed', { project: rule.shadowedBy })}</Badge>
                  {/if}
                </div>
              </td>
              <td class="actions">
                <Button size="sm" variant="ghost" onclick={() => openRule(rule)}>
                  {t('domainsPage.edit')}
                </Button>
                <Button size="sm" variant="danger" onclick={() => (removingRule = rule)}>
                  {t('common.delete')}
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>

<p class="footnote muted small">{t('domainsPage.dnsNote')}</p>

{#snippet problem()}
  <div class="failed small" role="alert">
    <strong>{t('domainsPage.rejected')}</strong>
    {#if formError.details.length}
      <ul>
        {#each formError.details as detail}<li>{detail}</li>{/each}
      </ul>
    {:else}
      <p>{formError.message}</p>
    {/if}
  </div>
{/snippet}

{#if addingParent}
  <Modal title={t('domainsPage.addDomainTitle')} onclose={() => (addingParent = false)}>
    <div class="form">
      <Field label={t('domainsPage.domain')} hint={t('domainsPage.domainHint')} required>
        {#snippet children(id)}
          <input
            {id}
            bind:value={parentDomain}
            placeholder="apps.example.com"
            autocomplete="off"
            spellcheck="false"
          />
        {/snippet}
      </Field>
      <Field label={t('vol.description')} hint={t('domainsPage.descriptionHint')}>
        {#snippet children(id)}
          <input {id} bind:value={parentDescription} autocomplete="off" />
        {/snippet}
      </Field>
      {#if formError}{@render problem()}{/if}

      {#if !formError && parentDomain.trim()}
        <div class="preview small">
          {t('domainsPage.addPreview', { host: `blog.${parentDomain.trim()}` })}
        </div>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (addingParent = false)}>{t('common.cancel')}</Button>
      <Button variant="primary" pending={saving} disabled={!parentDomain.trim()} onclick={addParent}>
        {t('domainsPage.register')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if editingProxy}
  <Modal
    title={editingProxy.isNew ? t('domainsPage.addProxyTitle') : t('domainsPage.editProxyTitle')}
    onclose={() => (editingProxy = null)}
  >
    <div class="form">
      <div class="two">
        <Field label={t('domainsPage.domain')} hint={t('domainsPage.proxyDomainHint')} required>
          {#snippet children(id)}
            <input
              {id}
              bind:value={pxDomain}
              placeholder="app.example.com"
              autocomplete="off"
              spellcheck="false"
            />
          {/snippet}
        </Field>
        <Field label={t('domainsPage.backend')} hint={t('domainsPage.backendHint')} required>
          {#snippet children(id)}
            <input
              {id}
              bind:value={pxTarget}
              placeholder="10.0.0.5:3000"
              autocomplete="off"
              spellcheck="false"
            />
          {/snippet}
        </Field>
      </div>

      <Field label={t('domainsPage.pathPrefix')} hint={t('domainsPage.proxyPathHint')}>
        {#snippet children(id)}
          <input {id} bind:value={pxPath} placeholder="/api" autocomplete="off" />
        {/snippet}
      </Field>

      {#if pxPath.trim()}
        <label class="check">
          <input type="checkbox" bind:checked={pxStripPath} />
          <span>
            <strong>{t('domainsPage.stripLabel')}</strong>
            <span class="small muted">{t('domainsPage.stripHint')}</span>
          </span>
        </label>
      {/if}

      <label class="check">
        <input type="checkbox" bind:checked={pxPassHost} />
        <span>
          <strong>{t('domainsPage.passHostLabel')}</strong>
          <span class="small muted">{t('domainsPage.passHostHint')}</span>
        </span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={pxInsecure} />
        <span>
          <strong>{t('domainsPage.insecureLabel')}</strong>
          <span class="small muted">{t('domainsPage.insecureHint')}</span>
        </span>
      </label>

      <Field label={t('vol.description')} hint={t('domainsPage.descriptionHint')}>
        {#snippet children(id)}
          <input {id} bind:value={pxDescription} autocomplete="off" />
        {/snippet}
      </Field>

      {#if formError}{@render problem()}{/if}

      {#if !formError}
        <div class="preview small">
          <code>https://{pxDomain.trim() || 'app.example.com'}{pxPath.trim()}</code>
          <span class="arrow" aria-hidden="true">→</span>
          <code>{proxyPreview}</code>
        </div>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (editingProxy = null)}>{t('common.cancel')}</Button>
      <Button
        variant="primary"
        pending={saving}
        disabled={!pxDomain.trim() || !pxTarget.trim()}
        onclick={saveProxy}
      >
        {t('common.save')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if removingProxy}
  <Confirm
    title={t('domainsPage.removeProxyTitle', { domain: removingProxy.domain })}
    message={t('domainsPage.removeProxyMessage', { target: removingProxy.resolvedTarget })}
    confirmLabel={t('common.delete')}
    danger
    onconfirm={removeProxy}
    onclose={() => (removingProxy = null)}
  />
{/if}

{#if editingRule}
  <Modal
    title={editingRule.isNew ? t('domainsPage.addRuleTitle') : t('domainsPage.editRuleTitle')}
    onclose={() => (editingRule = null)}
  >
    <div class="form">
      <div class="two">
        <Field label={t('domainsPage.from')} hint={t('domainsPage.fromHint')} required>
          {#snippet children(id)}
            <input
              {id}
              bind:value={ruleDomain}
              placeholder="old.example.com"
              autocomplete="off"
              spellcheck="false"
            />
          {/snippet}
        </Field>
        <Field label={t('domainsPage.to')} hint={t('domainsPage.toHint')} required>
          {#snippet children(id)}
            <input
              {id}
              bind:value={ruleTarget}
              placeholder="new.example.com"
              autocomplete="off"
              spellcheck="false"
            />
          {/snippet}
        </Field>
      </div>

      <Field label={t('domainsPage.pathPrefix')} hint={t('domainsPage.pathPrefixHint')}>
        {#snippet children(id)}
          <input {id} bind:value={rulePath} placeholder="/docs" autocomplete="off" />
        {/snippet}
      </Field>

      <label class="check">
        <input type="checkbox" bind:checked={ruleKeepPath} />
        <span>
          <strong>{t('domainsPage.keepPathLabel')}</strong>
          <span class="small muted">{t('domainsPage.keepPathHint')}</span>
        </span>
      </label>

      <label class="check">
        <input type="checkbox" bind:checked={rulePermanent} />
        <span>
          <strong>{t('domainsPage.permanentLabel')}</strong>
          <span class="small muted">{t('domainsPage.permanentHint')}</span>
        </span>
      </label>

      <Field label={t('vol.description')} hint={t('domainsPage.descriptionHint')}>
        {#snippet children(id)}
          <input {id} bind:value={ruleDescription} autocomplete="off" />
        {/snippet}
      </Field>

      {#if formError}{@render problem()}{/if}

      {#if !formError}
        <div class="preview small">
          <code>{rulePreview.from}</code>
          <span class="arrow" aria-hidden="true">→</span>
          <code>{rulePreview.to}</code>
        </div>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (editingRule = null)}>{t('common.cancel')}</Button>
      <Button
        variant="primary"
        pending={saving}
        disabled={!ruleDomain.trim() || !ruleTarget.trim()}
        onclick={saveRule}
      >
        {t('common.save')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if removingParent}
  <Confirm
    title={t('domainsPage.unregisterTitle', { domain: removingParent.domain })}
    message={removingParent.usedBy.length
      ? t('domainsPage.unregisterUsed', {
          count: removingParent.usedBy.length,
          list: removingParent.usedBy.join(', '),
        })
      : t('domainsPage.unregisterMessage')}
    confirmLabel={t('domainsPage.unregister')}
    danger
    onconfirm={removeParent}
    onclose={() => (removingParent = null)}
  />
{/if}

{#if removingRule}
  <Confirm
    title={t('domainsPage.removeRuleTitle', { domain: removingRule.domain })}
    message={t('domainsPage.removeRuleMessage', { target: removingRule.resolvedTarget })}
    confirmLabel={t('common.delete')}
    danger
    onconfirm={removeRule}
    onclose={() => (removingRule = null)}
  />
{/if}

<style>
  .cell {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
  }

  /* A name is an identifier, not prose: it may not be broken mid-word to
     make room for the long paths beside it. Those wrap instead. */
  .cell code {
    white-space: nowrap;
    overflow-wrap: normal;
  }

  /* A description belongs to the row above it, so it sits tight under the
     value rather than reading as its own line of data. */
  .sub {
    margin-top: 2px;
    font-size: 12px;
    color: var(--text-muted);
  }

  .pad { padding: 16px; }
  .target { color: var(--accent-text); }

  .footnote {
    margin: 4px 0 0;
    max-width: 78ch;
  }

  /* Cards on this screen stack, and nothing else gives them a gap. */
  :global(.card + .card) { margin-top: 14px; }

  .form { display: flex; flex-direction: column; gap: 14px; }

  .failed {
    padding: 9px 11px;
    background: var(--bad-bg);
    border-left: 3px solid var(--bad);
    border-radius: var(--radius-sm);
  }
  .failed strong { display: block; margin-bottom: 3px; }
  .failed p { margin: 0; white-space: pre-line; line-height: 1.5; }
  .failed ul {
    margin: 0;
    padding-left: 17px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
  @media (max-width: 620px) { .two { grid-template-columns: 1fr; } }

  .check { display: flex; align-items: flex-start; gap: 9px; cursor: pointer; }
  .check input { margin-top: 2px; flex: none; }
  .check span { display: flex; flex-direction: column; gap: 1px; }
  .check strong { font-size: 13px; font-weight: 550; }

  .preview {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    padding: 9px 11px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
    line-height: 1.5;
  }
  .preview code { background: var(--bg-raised); }
  .arrow { color: var(--text-faint); }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
</style>
