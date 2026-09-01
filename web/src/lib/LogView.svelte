<script>
  /**
   * A log viewer.
   *
   * The two behaviours that matter: it follows new output automatically, and
   * it stops following the instant you scroll up. Nothing is more annoying
   * than a log that yanks you back to the bottom while you are reading the
   * error you scrolled up to find.
   */
  let { lines = [], streaming = false, empty = 'No output yet.', height = '460px' } = $props();

  let container = $state(null);
  let following = $state(true);

  function onscroll() {
    if (!container) return;
    const distance = container.scrollHeight - container.scrollTop - container.clientHeight;
    following = distance < 40;
  }

  $effect(() => {
    // Depend on the line count so this re-runs as output arrives.
    lines.length;
    if (following && container) {
      container.scrollTop = container.scrollHeight;
    }
  });

  function toBottom() {
    following = true;
    if (container) container.scrollTop = container.scrollHeight;
  }
</script>

<div class="wrap" style:height>
  <!-- A scrollable region has to be focusable to be scrollable from the
       keyboard: without tabindex, Chrome offers no way to reach it at all.
       That is the accessible choice here, not a violation of one. -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="log mono"
    bind:this={container}
    {onscroll}
    tabindex="0"
    role="region"
    aria-label="Log output"
  >
    {#if lines.length === 0}
      <div class="placeholder">{empty}</div>
    {:else}
      <!-- Unkeyed: a log is append-only, so index identity is correct, and
           it stays robust if a stream ever re-delivers a sequence number. -->
      {#each lines as line}
        <div class="line {line.stream ?? 'stdout'}">
          {#if line.container}<span class="src">{line.container}</span>{/if}<span class="text"
            >{line.text}</span
          >
        </div>
      {/each}
    {/if}
    {#if streaming}
      <div class="line cursor"><span class="text">▋</span></div>
    {/if}
  </div>

  {#if !following}
    <button class="jump" onclick={toBottom}>Jump to latest ↓</button>
  {/if}
</div>

<style>
  .wrap { position: relative; }

  .log {
    height: 100%;
    overflow: auto;
    padding: 10px 12px;
    background: var(--bg-sunken);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    line-height: 1.55;
    /* Wrap rather than scroll sideways: a long stack trace read one
       horizontal scroll at a time is unreadable. */
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .placeholder { color: var(--text-faint); }

  .line { display: block; }
  .system { color: var(--accent-text); font-weight: 500; }
  .stderr { color: var(--bad); }
  .cursor { color: var(--text-faint); animation: blink 1s steps(2) infinite; }

  @keyframes blink { 50% { opacity: 0; } }

  .src {
    display: inline-block;
    min-width: 12ch;
    margin-right: 10px;
    color: var(--text-faint);
    user-select: none;
  }

  .jump {
    position: absolute;
    right: 14px;
    bottom: 12px;
    padding: 4px 10px;
    font-size: 12px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    box-shadow: var(--shadow);
    cursor: pointer;
  }
  .jump:hover { background: var(--bg-hover); }
</style>
