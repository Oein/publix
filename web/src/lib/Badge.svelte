<script>
  /** A status pill. `tone` comes from format.js so colour stays consistent. */
  let { tone = 'muted', dot = false, children } = $props();
</script>

<span class="badge {tone}">
  {#if dot}<span class="dot" class:pulse={tone === 'busy'} aria-hidden="true"></span>{/if}
  {@render children()}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 1px 8px;
    border-radius: 999px;
    font-size: 11.5px;
    font-weight: 550;
    letter-spacing: 0.01em;
    white-space: nowrap;
  }

  .good { background: var(--good-bg); color: var(--good); }
  .bad { background: var(--bad-bg); color: var(--bad); }
  .warn { background: var(--warn-bg); color: var(--warn); }
  .busy { background: var(--busy-bg); color: var(--busy); }
  .muted { background: var(--bg-sunken); color: var(--text-muted); }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
    flex: none;
  }

  /* Only a genuinely in-progress state animates, so movement always means
     "something is happening right now". */
  .pulse { animation: pulse 1.4s ease-in-out infinite; }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.35; }
  }
</style>
