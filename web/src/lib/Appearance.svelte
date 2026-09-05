<script>
  /**
   * Theme and language, behind one button in the topbar.
   *
   * Both are personal display preferences that live in this browser, so
   * they belong together and outside Settings — nothing here changes what
   * the server does, and a settings page that mixes the two invites
   * someone to think the language applies to everyone.
   */
  import { THEMES, theme, setTheme } from './theme.svelte.js';
  import { LANGUAGES, locale, setLocale, t } from './i18n.svelte.js';

  let open = $state(false);
  let root = $state(null);

  // A popover that survives a click elsewhere is a popover people close by
  // reloading the page.
  $effect(() => {
    if (!open) return;
    const away = (event) => {
      if (!root?.contains(event.target)) open = false;
    };
    const escape = (event) => {
      if (event.key === 'Escape') open = false;
    };
    document.addEventListener('pointerdown', away);
    document.addEventListener('keydown', escape);
    return () => {
      document.removeEventListener('pointerdown', away);
      document.removeEventListener('keydown', escape);
    };
  });

  const icons = {
    system: 'M4 5h16v10H4zM9 19h6M12 15v4',
    light:
      'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8M12 4V2.5M12 21.5V20M4 12H2.5M21.5 12H20M6.3 6.3 5.2 5.2M18.8 18.8l-1.1-1.1M17.7 6.3l1.1-1.1M5.2 18.8l1.1-1.1',
    dark: 'M20 13.5A8 8 0 1 1 10.5 4a6.5 6.5 0 0 0 9.5 9.5z',
  };
</script>

<div class="wrap" bind:this={root}>
  <button
    class="trigger"
    onclick={() => (open = !open)}
    aria-label={t('appearance.open')}
    title={t('appearance.open')}
    aria-expanded={open}
  >
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path
        d={icons[theme()]}
        fill={theme() === 'dark' ? 'currentColor' : 'none'}
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  </button>

  {#if open}
    <div class="pop">
      <p class="label">{t('appearance.theme')}</p>
      <div class="segments" role="group" aria-label={t('appearance.theme')}>
        {#each THEMES as option}
          <button
            class:on={theme() === option}
            onclick={() => setTheme(option)}
            aria-pressed={theme() === option}
          >
            <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
              <path
                d={icons[option]}
                fill={option === 'dark' ? 'currentColor' : 'none'}
                stroke="currentColor"
                stroke-width="1.6"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
            {t(`appearance.theme.${option}`)}
          </button>
        {/each}
      </div>

      <p class="label">{t('appearance.language')}</p>
      <ul class="langs">
        {#each LANGUAGES as language}
          <li>
            <button
              class:on={locale() === language.code}
              onclick={() => setLocale(language.code)}
              aria-pressed={locale() === language.code}
              lang={language.code}
            >
              <span class="grow">{language.label}</span>
              {#if locale() === language.code}<span class="tick" aria-hidden="true">✓</span>{/if}
            </button>
          </li>
        {/each}
      </ul>
    </div>
  {/if}
</div>

<style>
  .wrap { position: relative; }

  .trigger {
    display: flex;
    align-items: center;
    border: none;
    background: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 6px;
    border-radius: var(--radius-sm);
  }
  .trigger:hover { background: var(--bg-hover); color: var(--text); }

  .pop {
    position: absolute;
    right: 0;
    top: calc(100% + 6px);
    z-index: 30;
    width: 208px;
    padding: 10px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: var(--shadow-lg);
    animation: drop 0.12s ease-out;
  }

  @keyframes drop {
    from { opacity: 0; transform: translateY(-4px); }
    to { opacity: 1; transform: none; }
  }

  .label {
    margin: 0 0 6px;
    font-size: 11.5px;
    font-weight: 600;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--text-faint);
  }

  .segments {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 2px;
    padding: 2px;
    background: var(--bg-sunken);
    border-radius: var(--radius-sm);
    margin-bottom: 12px;
  }

  .segments button {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 3px;
    padding: 7px 2px;
    border: none;
    background: none;
    border-radius: 5px;
    color: var(--text-muted);
    font-size: 11.5px;
    cursor: pointer;
  }
  .segments button:hover { color: var(--text); }
  .segments button.on {
    background: var(--bg-raised);
    color: var(--text);
    box-shadow: var(--shadow);
  }

  .langs { list-style: none; margin: 0; padding: 0; }

  .langs button {
    display: flex;
    align-items: center;
    width: 100%;
    padding: 6px 8px;
    border: none;
    background: none;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    font-size: 13px;
    text-align: left;
    cursor: pointer;
  }
  .langs button:hover { background: var(--bg-hover); color: var(--text); }
  .langs button.on { color: var(--text); font-weight: 550; }

  .tick { color: var(--accent); }
</style>
