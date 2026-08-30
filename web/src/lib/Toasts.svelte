<script>
  import { toasts, dismiss } from './toast.js';

  let list = $state([]);
  toasts.subscribe((v) => (list = v));
</script>

<div class="stack" aria-live="polite">
  {#each list as toast (toast.id)}
    <div class="toast {toast.tone}">
      <div class="grow">
        <div class="message">{toast.message}</div>
        {#if toast.details?.length}
          <ul>
            {#each toast.details as detail}<li>{detail}</li>{/each}
          </ul>
        {/if}
      </div>
      <button onclick={() => dismiss(toast.id)} aria-label="Dismiss">×</button>
    </div>
  {/each}
</div>

<style>
  .stack {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 200;
    display: flex;
    flex-direction: column;
    gap: 8px;
    max-width: min(420px, calc(100vw - 32px));
  }

  .toast {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-left-width: 3px;
    border-radius: var(--radius-sm);
    background: var(--bg-raised);
    box-shadow: var(--shadow);
    animation: slide 0.16s ease-out;
  }

  @keyframes slide {
    from { opacity: 0; transform: translateX(12px); }
    to { opacity: 1; transform: none; }
  }

  .success { border-left-color: var(--good); }
  .error { border-left-color: var(--bad); }
  .info { border-left-color: var(--busy); }

  .message { font-size: 13px; line-height: 1.45; }

  ul {
    margin: 6px 0 0;
    padding-left: 16px;
    font-size: 12px;
    color: var(--text-muted);
    line-height: 1.5;
  }

  button {
    border: none;
    background: none;
    font-size: 18px;
    line-height: 1;
    color: var(--text-faint);
    cursor: pointer;
    padding: 0 2px;
  }
  button:hover { color: var(--text); }
</style>
