<script>
  let { title = '', description = '', children, actions = undefined } = $props();
</script>

<section class="card">
  {#if title}
    <header>
      <div class="grow">
        <h2>{title}</h2>
        {#if description}<p class="muted small">{description}</p>{/if}
      </div>
      {#if actions}<div class="row">{@render actions()}</div>{/if}
    </header>
  {/if}
  <div class="body">{@render children()}</div>
</section>

<style>
  .card {
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }

  header p { margin: 2px 0 0; }

  .body { padding: 16px; }
</style>
