<script>
  /**
   * The dashboard's button.
   *
   * `pending` is a first-class state rather than something each caller
   * reimplements: nearly every action here triggers something slow enough
   * to need it, and a button that silently does nothing for two seconds is
   * the fastest way to make someone click twice.
   */
  let {
    variant = 'default',
    size = 'md',
    pending = false,
    disabled = false,
    type = 'button',
    title = undefined,
    onclick = undefined,
    children,
  } = $props();
</script>

<button
  {type}
  {title}
  class="btn {variant} {size}"
  class:pending
  disabled={disabled || pending}
  {onclick}
>
  {#if pending}<span class="spinner" aria-hidden="true"></span>{/if}
  <span class="label">{@render children()}</span>
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    background: var(--bg-raised);
    color: var(--text);
    font-weight: 500;
    cursor: pointer;
    white-space: nowrap;
    transition: background 0.12s, border-color 0.12s, opacity 0.12s;
  }

  .md { padding: 6px 12px; font-size: 13px; }
  .sm { padding: 3px 9px; font-size: 12px; }
  .lg { padding: 9px 18px; font-size: 14px; }

  .btn:hover:not(:disabled) { background: var(--bg-hover); border-color: var(--text-faint); }
  .btn:active:not(:disabled) { transform: translateY(0.5px); }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  /* A pending button is working, not unavailable — keep it looking active. */
  .btn.pending { opacity: 0.85; cursor: progress; }

  .primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-fg);
  }
  .primary:hover:not(:disabled) { background: var(--accent-text); border-color: var(--accent-text); }

  .danger {
    background: transparent;
    border-color: var(--bad);
    color: var(--bad);
  }
  .danger:hover:not(:disabled) { background: var(--bad-bg); }

  .ghost {
    background: transparent;
    border-color: transparent;
    color: var(--text-muted);
  }
  .ghost:hover:not(:disabled) { background: var(--bg-hover); color: var(--text); border-color: transparent; }

  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: spin 0.6s linear infinite;
    flex: none;
  }

  @keyframes spin { to { transform: rotate(360deg); } }
</style>
