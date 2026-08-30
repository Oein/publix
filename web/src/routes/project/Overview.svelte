<script>
  import { api } from '../../lib/api.js';
  import { age, exact, duration, bytes, subject, statusTone, statusLabel } from '../../lib/format.js';
  import { notify } from '../../lib/toast.js';
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
  <Card title="Live deployment">
    {#if !live}
      <Empty
        title="Nothing deployed yet"
        description="Deploy this project and its containers will appear here."
      />
    {:else}
      <dl>
        <div>
          <dt>Status</dt>
          <dd><Badge tone={statusTone(live.status)} dot>{statusLabel(live.status)}</Badge></dd>
        </div>
        <div>
          <dt>Commit</dt>
          <dd>
            {#if live.short}
              <code>{live.short}</code>
              <span class="muted truncate">{subject(live.message)}</span>
            {:else}
              <span class="faint">Not from a commit</span>
            {/if}
          </dd>
        </div>
        {#if live.author}
          <div><dt>Author</dt><dd>{live.author}</dd></div>
        {/if}
        <div><dt>Branch</dt><dd>{live.branch || '—'}</dd></div>
        <div><dt>Trigger</dt><dd>{live.trigger}</dd></div>
        <div>
          <dt>Deployed</dt>
          <dd title={exact(live.finishedAt)}>
            {age(live.finishedAt)}
            <span class="faint">· took {duration(live.startedAt, live.finishedAt)}</span>
          </dd>
        </div>
        <div><dt>Build</dt><dd>{live.kind || '—'}</dd></div>
        {#if live.image}
          <div><dt>Image</dt><dd><code class="truncate">{live.image}</code></dd></div>
        {/if}
      </dl>
    {/if}
  </Card>

  <Card title="Addresses" description="Every hostname routed to this project.">
    {#if project.hosts?.length}
      <ul class="hosts">
        {#each project.hosts as host}
          <li>
            <a href="https://{host}" target="_blank" rel="noreferrer noopener">{host} ↗</a>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted small">
        No hostname yet. Add one under Domains, or set an apps domain in Settings and every
        project gets one automatically.
      </p>
    {/if}
  </Card>

  {#if project.volumes?.length}
    <Card title="Shared volumes" description="Server volumes this project mounts.">
      <ul class="vols">
        {#each project.volumes as vol}
          <li>
            <code>{vol}</code>
            <span class="faint small">→ /shared/{vol}</span>
          </li>
        {/each}
      </ul>
      <p class="muted small note">
        Each project gets its own directory on a shared volume, named after its project ID
        (<code>{project.id}</code>). Other projects cannot read it.
      </p>
    </Card>
  {/if}

  <Card title="Containers">
    {#snippet actions()}
      <Button size="sm" variant="ghost" onclick={loadContainers}>Refresh</Button>
    {/snippet}

    {#if containers === null}
      <p class="muted small">Loading…</p>
    {:else if containers.length === 0}
      <p class="muted small">No containers are running for this project.</p>
    {:else}
      <table>
        <thead>
          <tr>
            <th>Container</th>
            <th>State</th>
            <th class="num">CPU</th>
            <th class="num">Memory</th>
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
    {/if}
  </Card>
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
    gap: 14px;
    align-items: start;
  }

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
  td:first-child { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
</style>
