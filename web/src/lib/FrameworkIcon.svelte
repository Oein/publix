<script>
  /**
   * A mark for the language or framework a project is built with.
   *
   * These are deliberately simple geometric glyphs rather than brand
   * logos: a grid of twenty projects should be scannable, and twenty
   * full-colour logos is noise, not information. Each glyph is drawn in
   * its framework's accent so the shape and the colour reinforce each
   * other, on a neutral tile that stays quiet in both themes.
   */
  let { framework = '', size = 20, tile = true, title = '' } = $props();

  const accents = {
    nextjs: '#111827',
    nuxt: '#00b268',
    sveltekit: '#e5541f',
    remix: '#3b82f6',
    astro: '#8b5cf6',
    nestjs: '#dc2f4f',
    gatsby: '#7026b9',
    docusaurus: '#2e8555',
    angular: '#c3002f',
    cra: '#0891b2',
    vite: '#a855f7',
    node: '#3f8b3f',
    go: '#0a7d9e',
    python: '#3572a5',
    django: '#0c4b33',
    fastapi: '#029486',
    flask: '#444444',
    compose: '#1d63ed',
    dockerfile: '#1d63ed',
    static: '#b45309',
  };

  // In dark mode a near-black accent disappears; fall back to the text
  // colour for those rather than shipping two colour tables.
  const dim = new Set(['nextjs', 'flask']);

  const accent = $derived(accents[framework] ?? 'var(--text-muted)');
  const label = $derived(title || framework || 'unknown');
</script>

<span
  class="mark"
  class:tile
  class:dim={dim.has(framework)}
  style:--accent={accent}
  style:--size="{size}px"
  role="img"
  aria-label={label}
  {title}
>
  <svg viewBox="0 0 24 24" width={size} height={size} fill="none" aria-hidden="true">
    {#if framework === 'nextjs'}
      <circle cx="12" cy="12" r="9.25" stroke="currentColor" stroke-width="1.6" />
      <path d="M9 16V8l6.5 8.5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
      <path d="M15 8v5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
    {:else if framework === 'nuxt'}
      <path
        d="M2.5 18h19L15 6.5 11.6 12M2.5 18l6-10 3 5"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linejoin="round"
        stroke-linecap="round"
      />
    {:else if framework === 'sveltekit'}
      <path
        d="M15.5 5.5c-2.4-1.5-5.3-.9-6.7 1.3l-3 4.7c-1.4 2.2-.8 5 1.4 6.4 2.4 1.5 5.3.9 6.7-1.3l3-4.7c1.4-2.2.8-5-1.4-6.4Z"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linejoin="round"
      />
      <path d="M9.8 14.6c1.6.8 3.3.4 4.1-.8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
    {:else if framework === 'remix'}
      <path
        d="M5 5h8.5c2.2 0 3.5 1.1 3.5 2.9 0 1.6-1 2.6-2.6 2.9 1.8.2 2.7 1.1 2.7 3V19"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path d="M5 11.5h9" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" />
      <path d="M5 19v-4h6" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" />
    {:else if framework === 'astro'}
      <path d="M12 3 7 19l5-2.8L17 19 12 3Z" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
      <path d="M8.5 15.5c1.5 2 5.5 2 7 0" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
    {:else if framework === 'angular'}
      <path d="M12 3 3.8 6l1.3 10.4L12 21l6.9-4.6L20.2 6 12 3Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
      <path d="M8.6 15 12 7l3.4 8M9.9 12.6h4.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" />
    {:else if framework === 'cra'}
      <circle cx="12" cy="12" r="2" fill="currentColor" />
      <ellipse cx="12" cy="12" rx="9.5" ry="3.8" stroke="currentColor" stroke-width="1.4" />
      <ellipse cx="12" cy="12" rx="9.5" ry="3.8" stroke="currentColor" stroke-width="1.4" transform="rotate(60 12 12)" />
      <ellipse cx="12" cy="12" rx="9.5" ry="3.8" stroke="currentColor" stroke-width="1.4" transform="rotate(120 12 12)" />
    {:else if framework === 'vite'}
      <path d="M3 5.5 12 21 21 5.5 12 8 3 5.5Z" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
      <path d="M13 6.5 10.5 12h2.2l-1.2 4.5L15 10h-2.3l.3-3.5Z" fill="currentColor" />
    {:else if framework === 'gatsby'}
      <circle cx="12" cy="12" r="9.25" stroke="currentColor" stroke-width="1.6" />
      <path d="M12 7.5h4.5M12 12h4.5a4.5 4.5 0 0 1-4.5 4.5v-9a4.5 4.5 0 0 0 0 9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
    {:else if framework === 'docusaurus'}
      <path d="M4 5.5h6a2 2 0 0 1 2 2V19a2.5 2.5 0 0 0-2.5-2H4v-11Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
      <path d="M20 5.5h-6a2 2 0 0 0-2 2V19a2.5 2.5 0 0 1 2.5-2H20v-11Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
    {:else if framework === 'node'}
      <path d="M12 2.8 20.5 7.4v9.2L12 21.2 3.5 16.6V7.4L12 2.8Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
      <path d="M9 15.5V9.2l6 5.6V9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
    {:else if framework === 'nestjs'}
      <path d="M12 2.8 20 6v6.4c0 4.3-3.3 7.6-8 8.8-4.7-1.2-8-4.5-8-8.8V6l8-3.2Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
      <path d="M9.3 15.6V8.9l5.4 5v-5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
    {:else if framework === 'go'}
      <!-- Go's wordmark, with the speed lines that go with it. A geometric
           glyph for Go reads as a dial or a gauge, not as Go. -->
      <path d="M1.5 9.5h3.2M.8 12h3.9M1.5 14.5h3.2" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
      <text
        x="14.6"
        y="16.1"
        text-anchor="middle"
        font-size="10.5"
        font-weight="700"
        letter-spacing="-0.5"
        fill="currentColor"
        font-family="ui-sans-serif, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
      >GO</text>
    {:else if framework === 'python' || framework === 'django' || framework === 'fastapi' || framework === 'flask'}
      <path
        d="M12 2.8c-2.8 0-4.6.9-4.6 3v2.4h4.8v.9H5.6c-2 0-3.1 1.5-3.1 4s1 4 3.1 4h1.6v-2.6c0-2 1.6-3.5 3.6-3.5h3.6c1.7 0 3-1.3 3-3V5.8c0-1.8-1.6-3-3.4-3H12Z"
        stroke="currentColor"
        stroke-width="1.4"
        stroke-linejoin="round"
      />
      <path
        d="M12 21.2c2.8 0 4.6-.9 4.6-3v-2.4h-4.8v-.9h6.6c2 0 3.1-1.5 3.1-4s-1-4-3.1-4h-1.6v2.6c0 2-1.6 3.5-3.6 3.5h-3.6c-1.7 0-3 1.3-3 3v2.2c0 1.8 1.6 3 3.4 3H12Z"
        stroke="currentColor"
        stroke-width="1.4"
        stroke-linejoin="round"
      />
    {:else if framework === 'compose'}
      <path d="M3.5 8 12 4l8.5 4-8.5 4-8.5-4Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
      <path d="M3.5 12 12 16l8.5-4M3.5 16 12 20l8.5-4" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
    {:else if framework === 'dockerfile'}
      <rect x="4" y="11" width="3.2" height="3.2" stroke="currentColor" stroke-width="1.4" />
      <rect x="8" y="11" width="3.2" height="3.2" stroke="currentColor" stroke-width="1.4" />
      <rect x="12" y="11" width="3.2" height="3.2" stroke="currentColor" stroke-width="1.4" />
      <rect x="8" y="7.2" width="3.2" height="3.2" stroke="currentColor" stroke-width="1.4" />
      <path d="M2.5 15.5c3 3.5 12 3.5 15.5-1 2 .6 3 0 3.5-1-1-.4-2-.3-2.6.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
    {:else if framework === 'static'}
      <path d="M9 8.5 4.5 12 9 15.5M15 8.5 19.5 12 15 15.5M13.4 6l-2.8 12" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" />
    {:else}
      <rect x="4" y="4" width="16" height="16" rx="3.5" stroke="currentColor" stroke-width="1.5" />
      <path d="M12 15.5v.01M12 8.5a2 2 0 0 1 1.2 3.6c-.7.5-1.2.9-1.2 1.6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
    {/if}
  </svg>
</span>

<style>
  .mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--accent);
    flex: none;
  }

  .tile {
    width: calc(var(--size) + 12px);
    height: calc(var(--size) + 12px);
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  /* A near-black accent vanishes on a dark background. The :not() guard
     lets an explicit light choice win over an OS that prefers dark. */
  @media (prefers-color-scheme: dark) {
    :global(:root:not([data-theme='light'])) .dim { color: var(--text); }
  }
  :global(:root[data-theme='dark']) .dim { color: var(--text); }
</style>
