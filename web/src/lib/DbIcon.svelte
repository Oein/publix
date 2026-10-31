<script>
  /**
   * A mark for a database engine, so a list of databases can be scanned by
   * kind before it is read. Simple line glyphs in the spirit of each
   * project's own emblem — an elephant, a dolphin, a leaf, a stack, a
   * feather — drawn in the engine's colour, like FrameworkIcon.
   */
  let { engine = '', size = 18, tile = true, title = '' } = $props();

  const known = new Set(['postgres', 'mysql', 'mongo', 'redis', 'sqlite']);
  const names = { postgres: 'PostgreSQL', mysql: 'MySQL', mongo: 'MongoDB', redis: 'Redis', sqlite: 'SQLite' };
  const label = $derived(title || names[engine] || engine || 'database');
</script>

<span
  class="mark {known.has(engine) ? engine : 'other'}"
  class:tile
  style:--size="{size}px"
  role="img"
  aria-label={label}
  title={label}
>
  <svg
    viewBox="0 0 24 24"
    width={size}
    height={size}
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    {#if engine === 'postgres'}
      <path d="M6.2 15.6C4.8 14.3 4 12.5 4 10.4 4 6.7 7.5 4 12 4s8 2.7 8 6.4c0 2.1-.8 3.9-2.2 5.2" />
      <path d="M12 11.5v6.3c0 1.3.9 2.2 2.1 2.2 1 0 1.7-.6 1.9-1.5" />
      <path d="M9.5 11.8c.6 1.2 1.4 1.8 2.5 1.8s1.9-.6 2.5-1.8" />
      <path d="M4.4 9.2C3 9.6 2.2 10.8 2.6 12.4c.3 1.2 1.3 2 2.6 2" />
      <path d="M19.6 9.2c1.4.4 2.2 1.6 1.8 3.2-.3 1.2-1.3 2-2.6 2" />
      <circle cx="9" cy="9" r=".9" fill="currentColor" stroke="none" />
      <circle cx="15" cy="9" r=".9" fill="currentColor" stroke="none" />
    {:else if engine === 'mysql'}
      <path d="M3 18.5c1.6-5.6 6-9.6 11.6-10 2.6-.2 4.6.6 6.4 2.2-1.6.4-2.6.4-3.8 0" />
      <path d="M17.4 10.7c-.5 2.9-3.2 5-6.8 5.1-2.3.1-4.3.9-5.8 2.7" />
      <path d="M10.5 9.2 11.2 5.5l2.6 2.9" />
      <path d="M3 18.5 1.8 21M4.8 18.5l1.6 2.2" />
      <circle cx="17.6" cy="9.2" r=".7" fill="currentColor" stroke="none" />
    {:else if engine === 'mongo'}
      <path d="M12 2.8c3.7 3.9 5.3 7.4 5.3 10.5 0 3.7-2.4 6.2-5.3 7.1-2.9-.9-5.3-3.4-5.3-7.1 0-3.1 1.6-6.6 5.3-10.5z" />
      <path d="M12 6v15.6" />
    {:else if engine === 'redis'}
      <path d="M12 4.2 20.5 8 12 11.8 3.5 8z" />
      <path d="M3.5 12 12 15.8 20.5 12" />
      <path d="M3.5 16 12 19.8 20.5 16" />
    {:else if engine === 'sqlite'}
      <path d="M19.5 3.5c-7.6.8-11.6 5.6-12.3 13.2l.6.6c7.6-.7 12.4-4.7 13.2-12.3z" />
      <path d="M4 20 15 9" />
      <path d="M10.5 13.5h4M12.5 11.5h3.6" />
    {:else}
      <ellipse cx="12" cy="6" rx="7" ry="2.6" />
      <path d="M5 6v12c0 1.4 3.1 2.6 7 2.6s7-1.2 7-2.6V6M5 12c0 1.4 3.1 2.6 7 2.6s7-1.2 7-2.6" />
    {/if}
  </svg>
</span>

<style>
  .mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    color: var(--db-color);
    --db-color: var(--text-muted);
  }
  .postgres { --db-color: #336791; }
  .mysql { --db-color: #00758f; }
  .mongo { --db-color: #3f9142; }
  .redis { --db-color: #d33a2c; }
  .sqlite { --db-color: #0f80cc; }

  .tile {
    width: calc(var(--size) + 12px);
    height: calc(var(--size) + 12px);
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  /* The brand colours are dark; lift them on a dark background. */
  @media (prefers-color-scheme: dark) {
    :global(:root:not([data-theme='light'])) .postgres { --db-color: #7aa9dc; }
    :global(:root:not([data-theme='light'])) .mysql { --db-color: #4fc0d6; }
    :global(:root:not([data-theme='light'])) .mongo { --db-color: #6cc26a; }
    :global(:root:not([data-theme='light'])) .redis { --db-color: #ff7468; }
    :global(:root:not([data-theme='light'])) .sqlite { --db-color: #5bb0f0; }
  }
  :global(:root[data-theme='dark']) .postgres { --db-color: #7aa9dc; }
  :global(:root[data-theme='dark']) .mysql { --db-color: #4fc0d6; }
  :global(:root[data-theme='dark']) .mongo { --db-color: #6cc26a; }
  :global(:root[data-theme='dark']) .redis { --db-color: #ff7468; }
  :global(:root[data-theme='dark']) .sqlite { --db-color: #5bb0f0; }
</style>
