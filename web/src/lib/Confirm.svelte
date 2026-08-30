<script>
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';

  /**
   * A confirmation dialog.
   *
   * `confirmText` requires the user to type something exact before the
   * destructive action unlocks. It is reserved for things that cannot be
   * undone — deleting a project — and deliberately not used for anything
   * that can be, so the friction still means something when it appears.
   */
  let {
    title,
    message,
    confirmLabel = 'Confirm',
    confirmText = '',
    danger = false,
    onconfirm,
    onclose,
    children = undefined,
  } = $props();

  let typed = $state('');
  let pending = $state(false);

  const unlocked = $derived(!confirmText || typed === confirmText);

  async function run() {
    pending = true;
    try {
      await onconfirm();
    } finally {
      pending = false;
    }
  }
</script>

<Modal {title} {onclose}>
  <p>{message}</p>
  {#if children}{@render children()}{/if}
  {#if confirmText}
    <p class="small muted">Type <code>{confirmText}</code> to confirm.</p>
    <input
      bind:value={typed}
      placeholder={confirmText}
      autocomplete="off"
      spellcheck="false"
      onkeydown={(e) => e.key === 'Enter' && unlocked && run()}
    />
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={onclose}>Cancel</Button>
    <Button
      variant={danger ? 'danger' : 'primary'}
      disabled={!unlocked}
      {pending}
      onclick={run}
    >
      {confirmLabel}
    </Button>
  {/snippet}
</Modal>

<style>
  input {
    width: 100%;
    margin-top: 6px;
    padding: 6px 9px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-family: var(--mono);
    font-size: 13px;
  }
  input:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  code {
    padding: 1px 5px;
    background: var(--bg-sunken);
    border-radius: 3px;
  }
</style>
