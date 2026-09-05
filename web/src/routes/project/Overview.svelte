<script>
  import { api } from '../../lib/api.js';
  import { age, exact, duration, bytes, subject, statusTone, statusLabel } from '../../lib/format.js';
  import { notify } from '../../lib/toast.js';
  import { t, tparts } from '../../lib/i18n.svelte.js';
  import Badge from '../../lib/Badge.svelte';
  import Card from '../../lib/Card.svelte';
  import Empty from '../../lib/Empty.svelte';
  import Button from '../../lib/Button.svelte';

  let { project, revision, onchange } = $props();

  let containers = $state(null);

  async function loadContainers() {
    try {
      containers = await api.projects.containers(project.id);
    } catch (err) {
      containers = [];
    }
  }

  $effect(() => {
    revision;
    project.id;
    loadContainers();
  });

  // Resource numbers go stale fast enough to be misleading; refresh them
  // while the tab is open, and stop when it is not.
  $effect(() => {
    const timer = setInterval(loadContainers, 8000);
    return () => clearInterval(timer);
  });

  const live = $derived(project.live);
</script>

<div class="grid">
  <Card title={t('overview.live')}>
    {#if !live}
      <Empty title={t('overview.emptyTitle')} description={t('overview.emptyDesc')} />
    {:else}
      <dl>
        <div>
          <dt>{t('overview.status')}</dt>
          <dd><Badge tone={statusTone(live.status)} dot>{statusLabel(live.status)}</Badge></dd>
        </div>
        <div>
          <dt>{t('overview.commit')}</dt>
          <dd>
            {#if live.short}
              <code>{live.short}</code>
              <span class="muted truncate">{subject(live.message)}</span>
            {:else}
              <span class="faint">{t('overview.notFromCommit')}</span>
            {/if}
          </dd>
        </div>
        {#if live.author}
          <div><dt>{t('overview.author')}</dt><dd>{live.author}</dd></div>
        {/if}
        <div><dt>{t('overview.branch')}</dt><dd>{live.branch || '—'}</dd></div>
        <div><dt>{t('overview.trigger')}</dt><dd>{live.trigger}</dd></div>
        <div>
          <dt>{t('overview.deployed')}</dt>
          <dd title={exact(live.finishedAt)}>
            {age(live.finishedAt)}
            <span class="faint">
              · {t('overview.took', { time: duration(live.startedAt, live.finishedAt) })}
            </span>
          </dd>
        </div>
        <div><dt>{t('overview.build')}</dt><dd>{live.kind || '—'}</dd></div>
        {#if live.image}
          <div><dt>{t('overview.image')}</dt><dd><code class="truncate">{live.image}</code></dd></div>
        {/if}
      </dl>
    {/if}
  </Card>

  <Card title={t('overview.addresses')} description={t('overview.addressesDesc')}>
    {#if project.hosts?.length}
      <ul class="hosts">
        {#each project.hosts as host}
          <li>
            <a href="https://{host}" target="_blank" rel="noreferrer noopener">{host} ↗</a>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted small">{t('overview.noHostname')}</p>
    {/if}
  </Card>

  {#if project.volumes?.length}
    <Card title={t('overview.volumes')} description={t('overview.volumesDesc')}>
      <ul class="vols">
        {#each project.volumes as vol}
          <li>
            <code>{vol}</code>
            <span class="faint small">→ /shared/{vol}</span>
          </li>
        {/each}
      </ul>
      <p class="muted small note">
        {#each tparts('overview.volumesNote') as part}{#if part.slot}<code>{project.id}</code
          >{:else}{part.text}{/if}{/each}
      </p>
    </Card>
  {/if}

  <div class="full">
  <Card title={t('overview.containers')}>
    {#snippet actions()}
      <Button size="sm" variant="ghost" onclick={loadContainers}>{t('common.refresh')}</Button>
    {/snippet}

    {#if containers === null}
      <p class="muted small">{t('common.loading')}</p>
    {:else if containers.length === 0}
      <p class="muted small">{t('overview.noContainers')}</p>
    {:else}
      <div class="scroll">
      <table>
        <thead>
          <tr>
            <th>{t('overview.container')}</th>
            <th>{t('overview.state')}</th>
            <th class="num">{t('overview.cpu')}</th>
            <th class="num">{t('overview.memory')}</th>
          </tr>
        </thead>
        <tbody>
          {#each containers as c (c.id)}
            <tr>
              <td>
                <code class="truncate">{c.name}</code>
                {#if c.service}<span class="faint small">{c.service}</span>{/if}
              </td>
              <td>
                <Badge tone={c.state === 'running' ? 'good' : 'muted'} dot>{c.state}</Badge>
              </td>
              <td class="num">{c.state === 'running' ? `${c.cpu.toFixed(1)}%` : '—'}</td>
              <td class="num">
                {#if c.state === 'running' && c.memory}
                  {bytes(c.memory)}
                {:else}—{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      </div>
    {/if}
  </Card>
  </div>
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
    gap: 14px;
    align-items: start;
  }

  /* The container table needs the whole row: squeezed into an auto-fit
     column its right-hand values get clipped. */
  .full { grid-column: 1 / -1; }

  /* And on a narrow window it scrolls rather than the page doing so. */
  .scroll { overflow-x: auto; }

  dl { margin: 0; display: flex; flex-direction: column; gap: 8px; }
  dl div { display: flex; gap: 12px; align-items: baseline; font-size: 13px; }
  dt { min-width: 84px; color: var(--text-muted); flex: none; }
  dd { margin: 0; display: flex; align-items: center; gap: 7px; min-width: 0; }

  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
    font-family: var(--mono);
    font-size: 11.5px;
  }

  .hosts, .vols { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .vols li { display: flex; align-items: center; gap: 8px; }

  .note { margin-top: 12px; line-height: 1.5; }

  table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
  th {
    text-align: left;
    padding: 0 0 7px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--text-faint);
    border-bottom: 1px solid var(--border);
  }
  td { padding: 8px 0; border-bottom: 1px solid var(--border); }
  tr:last-child td { border-bottom: none; }
  /* align-items keeps the name chip hugging its text instead of stretching
     across the whole column. */
  td:first-child {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
    min-width: 0;
  }
  th:first-child, td:first-child { padding-right: 16px; }
  /* Right-aligned columns need their own gutter, or two of them side by
     side read as one word. */
  .num { text-align: right; font-variant-numeric: tabular-nums; padding-left: 16px; }
  th.num { padding-left: 16px; }
</style>
