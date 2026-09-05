<script>
  import { api, setUnauthorizedHandler, eventStream } from './lib/api.js';
  import { route, navigate } from './lib/router.js';
  import { notify } from './lib/toast.js';
  import { t } from './lib/i18n.svelte.js';
  import Appearance from './lib/Appearance.svelte';
  import Toasts from './lib/Toasts.svelte';
  import Login from './routes/Login.svelte';
  import Projects from './routes/Projects.svelte';
  import Project from './routes/Project.svelte';
  import Import from './routes/Import.svelte';
  import Settings from './routes/Settings.svelte';

  let auth = $state(null);
  let current = $state({ path: '/', segments: [], query: new URLSearchParams() });
  route.subscribe((r) => (current = r));

  // A monotonically increasing counter that screens watch to know the
  // server's state changed. Passing a number rather than the payload keeps
  // each screen in charge of what it refetches.
  let revision = $state(0);

  setUnauthorizedHandler(() => {
    auth = { needsSetup: false, authenticated: false };
  });

  async function loadAuth() {
    try {
      auth = await api.auth.state();
    } catch (err) {
      notify.error(err);
      auth = { needsSetup: false, authenticated: false };
    }
  }

  loadAuth();

  $effect(() => {
    if (!auth?.authenticated) return;
    // One event stream for the whole app: every screen reacts to the same
    // revision counter, so a deploy started in one tab updates the list in
    // another without any screen polling.
    return eventStream({
      change: () => (revision += 1),
      error: () => {},
    });
  });

  async function logout() {
    try {
      await api.auth.logout();
    } catch {
      // Signing out locally is the right outcome even if the request failed.
    }
    auth = { needsSetup: false, authenticated: false };
    navigate('/');
  }

  const nav = [
    { key: 'projects', path: '/', match: (p) => p === '/' || p.startsWith('/projects') },
    { key: 'import', path: '/import', match: (p) => p.startsWith('/import') },
    { key: 'settings', path: '/settings', match: (p) => p.startsWith('/settings') },
  ];
</script>

<Toasts />

{#if auth === null}
  <div class="boot"><span class="spinner"></span></div>
{:else if !auth.authenticated}
  <Login needsSetup={auth.needsSetup} onauthenticated={loadAuth} />
{:else}
  <div class="shell">
    <header class="topbar">
      <a class="brand" href="#/">
        <svg viewBox="0 0 100 100" width="20" height="20" aria-hidden="true">
          <rect width="100" height="100" rx="22" fill="currentColor" opacity="0.12" />
          <path
            d="M32 74V26h20a16 16 0 0 1 0 32H42"
            stroke="currentColor"
            stroke-width="9"
            fill="none"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
        <span>publix</span>
      </a>

      <nav>
        {#each nav as item}
          <a href="#{item.path}" class:active={item.match(current.path)}>{t(`nav.${item.key}`)}</a>
        {/each}
      </nav>

      <Appearance />
      <button class="signout" onclick={logout}>{t('app.signOut')}</button>
    </header>

    <main>
      {#if current.segments[0] === 'projects' && current.segments[1]}
        {#key current.segments[1]}
          <Project id={current.segments[1]} tab={current.segments[2] ?? 'overview'} {revision} />
        {/key}
      {:else if current.segments[0] === 'import'}
        <Import {revision} />
      {:else if current.segments[0] === 'settings'}
        <Settings {revision} />
      {:else}
        <Projects {revision} />
      {/if}
    </main>
  </div>
{/if}

<style>
  .boot {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
  }

  .spinner {
    width: 22px;
    height: 22px;
    border: 2px solid var(--border-strong);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
  }

  @keyframes spin { to { transform: rotate(360deg); } }

  .shell { min-height: 100vh; }

  .topbar {
    position: sticky;
    top: 0;
    z-index: 20;
    display: flex;
    align-items: center;
    gap: 20px;
    padding: 0 20px;
    height: 52px;
    background: var(--bg-raised);
    border-bottom: 1px solid var(--border);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--text);
    font-weight: 650;
    font-size: 15px;
    letter-spacing: -0.02em;
  }
  .brand:hover { text-decoration: none; }

  nav {
    display: flex;
    gap: 2px;
    flex: 1;
  }

  nav a {
    padding: 5px 11px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
  }
  nav a:hover { background: var(--bg-hover); color: var(--text); text-decoration: none; }
  nav a.active { background: var(--bg-sunken); color: var(--text); }

  .signout {
    border: none;
    background: none;
    color: var(--text-muted);
    font-size: 13px;
    cursor: pointer;
    padding: 5px 8px;
    border-radius: var(--radius-sm);
  }
  .signout:hover { background: var(--bg-hover); color: var(--text); }

  main {
    max-width: 1120px;
    margin: 0 auto;
    padding: 24px 20px 64px;
  }

  @media (max-width: 640px) {
    .topbar { gap: 12px; padding: 0 12px; }
    .brand span { display: none; }
    main { padding: 16px 12px 48px; }
  }
</style>
