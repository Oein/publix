<script>
  /**
   * A section that stays shut until it is wanted, with a one-line summary
   * of what is inside it.
   *
   * A form that shows every field at once makes the operator read all of
   * them to find the two they care about. The summary is what makes
   * collapsing honest: a closed section still says what it currently
   * holds, so nothing is hidden — only folded.
   */
  let { label, summary = '', open = $bindable(false), children } = $props();
</script>

<div class="disclosure" class:open>
  <button type="button" class="head" aria-expanded={open} onclick={() => (open = !open)}>
    <span class="caret" aria-hidden="true">{open ? '▾' : '▸'}</span>
    <strong class="small">{label}</strong>
    {#if summary && !open}<span class="small muted summary truncate">{summary}</span>{/if}
  </button>
  {#if open}
    <div class="body">{@render children?.()}</div>
  {/if}
</div>

<style>
  .disclosure {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg);
  }
  .disclosure + :global(.disclosure) { margin-top: 8px; }

  .head {
    display: flex;
    align-items: baseline;
    gap: 8px;
    width: 100%;
    padding: 9px 11px;
    background: none;
    border: 0;
    cursor: pointer;
    text-align: left;
    color: var(--text);
    font: inherit;
    border-radius: var(--radius-sm);
  }
  .head:hover { background: var(--bg-sunken); }
  .caret { color: var(--text-muted); font-size: 10px; }
  .summary { margin-left: auto; max-width: 55%; text-align: right; }

  .body {
    padding: 4px 11px 12px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
</style>
