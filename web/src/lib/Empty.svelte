<script>
  /**
   * An empty state.
   *
   * Every one of these says what to do next, because "no projects" on its
   * own is a dead end for someone who has just installed this.
   */
  let { title, description = '', children = undefined } = $props();
</script>

<div class="empty">
  <h3>{title}</h3>
  {#if description}<p class="muted">{description}</p>{/if}
  {#if children}<div class="actions">{@render children()}</div>{/if}
</div>

<style>
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 44px 24px;
    text-align: center;
  }

  .empty p { max-width: 42ch; line-height: 1.55; }
  .actions { margin-top: 10px; }
</style>
