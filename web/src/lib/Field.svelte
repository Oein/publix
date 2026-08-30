<script>
  /**
   * A labelled form control.
   *
   * `hint` sits under the label rather than in a tooltip: configuration
   * screens are read once, carefully, and hiding the explanation behind a
   * hover is how people misconfigure things.
   */
  let { label, hint = '', error = '', required = false, children } = $props();
  const id = `f${Math.random().toString(36).slice(2, 9)}`;
</script>

<div class="field">
  <label for={id}>
    {label}{#if required}<span class="req" aria-hidden="true">*</span>{/if}
  </label>
  {#if hint}<p class="hint">{hint}</p>{/if}
  <div class="control">{@render children(id)}</div>
  {#if error}<p class="error">{error}</p>{/if}
</div>

<style>
  .field { display: flex; flex-direction: column; gap: 4px; }

  label {
    font-size: 12.5px;
    font-weight: 550;
    color: var(--text);
  }

  .req { color: var(--bad); margin-left: 2px; }

  .hint {
    margin: 0 0 2px;
    font-size: 12px;
    color: var(--text-muted);
    line-height: 1.45;
  }

  .error {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--bad);
  }

  .control :global(input),
  .control :global(select),
  .control :global(textarea) {
    width: 100%;
    padding: 6px 9px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 13px;
  }

  .control :global(input:focus),
  .control :global(select:focus),
  .control :global(textarea:focus) {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .control :global(textarea) {
    font-family: var(--mono);
    font-size: 12.5px;
    resize: vertical;
    min-height: 90px;
  }

  .control :global(input::placeholder),
  .control :global(textarea::placeholder) { color: var(--text-faint); }
</style>
