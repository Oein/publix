<script>
  /** A dialog. Escape and backdrop both close it; focus is trapped inside. */
  let { title, wide = false, onclose, children, footer = undefined } = $props();

  let dialog = $state(null);

  $effect(() => {
    // Focus the first control so the keyboard works immediately, and so a
    // screen reader lands inside the dialog rather than behind it.
    const target = dialog?.querySelector('input, select, textarea, button');
    target?.focus();
  });

  function onkeydown(event) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      onclose();
      return;
    }
    if (event.key !== 'Tab' || !dialog) return;

    const focusable = [...dialog.querySelectorAll(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )];
    if (focusable.length === 0) return;

    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<svelte:window on:keydown={onkeydown} />

<div
  class="backdrop"
  role="presentation"
  onclick={(e) => e.target === e.currentTarget && onclose()}
>
  <div class="dialog" class:wide bind:this={dialog} role="dialog" aria-modal="true" aria-label={title}>
    <header>
      <h2>{title}</h2>
      <button class="close" onclick={onclose} aria-label="Close">×</button>
    </header>
    <div class="body">{@render children()}</div>
    {#if footer}<footer>{@render footer()}</footer>{/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 100;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: 8vh 16px 24px;
    background: rgb(8 10 14 / 55%);
    backdrop-filter: blur(2px);
    overflow-y: auto;
  }

  .dialog {
    width: 100%;
    max-width: 480px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: var(--shadow-lg);
    animation: rise 0.14s ease-out;
  }

  .wide { max-width: 780px; }

  @keyframes rise {
    from { opacity: 0; transform: translateY(6px); }
    to { opacity: 1; transform: none; }
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }

  .close {
    border: none;
    background: none;
    font-size: 22px;
    line-height: 1;
    color: var(--text-muted);
    cursor: pointer;
    padding: 0 4px;
    border-radius: 4px;
  }
  .close:hover { color: var(--text); background: var(--bg-hover); }

  .body { padding: 16px; }

  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--border);
    background: var(--bg-sunken);
  }
</style>
