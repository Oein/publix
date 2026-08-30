<script>
  import { route } from '../lib/router.js';
  import ServerSettings from './settings/Server.svelte';
  import Volumes from './settings/Volumes.svelte';
  import GitHubSettings from './settings/GitHub.svelte';
  import Account from './settings/Account.svelte';

  let { revision } = $props();

  let section = $state('server');
  route.subscribe((r) => (section = r.segments[1] ?? 'server'));

  const tabs = [
    { key: 'server', label: 'Server' },
    { key: 'volumes', label: 'Shared volumes' },
    { key: 'github', label: 'GitHub' },
    { key: 'account', label: 'Account' },
  ];
</script>

<h1>Settings</h1>

<nav class="tabs">
  {#each tabs as t}
    <a href="#/settings/{t.key}" class:active={section === t.key}>{t.label}</a>
  {/each}
</nav>

{#if section === 'volumes'}
  <Volumes {revision} />
{:else if section === 'github'}
  <GitHubSettings {revision} />
{:else if section === 'account'}
  <Account />
{:else}
  <ServerSettings {revision} />
{/if}

<style>
  h1 { margin-bottom: 14px; }

  .tabs {
    display: flex;
    gap: 2px;
    margin-bottom: 20px;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
  }

  .tabs a {
    padding: 7px 12px;
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
    white-space: nowrap;
  }
  .tabs a:hover { color: var(--text); text-decoration: none; }
  .tabs a.active { color: var(--text); border-bottom-color: var(--accent); }
</style>
