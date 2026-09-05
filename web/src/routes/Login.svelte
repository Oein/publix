<script>
  import { api } from '../lib/api.js';
  import Button from '../lib/Button.svelte';
  import { t } from '../lib/i18n.svelte.js';
  import Appearance from '../lib/Appearance.svelte';

  let { needsSetup, onauthenticated } = $props();

  let password = $state('');
  let confirm = $state('');
  let pending = $state(false);
  let error = $state('');

  async function submit(event) {
    event.preventDefault();
    error = '';

    if (needsSetup && password !== confirm) {
      error = t('login.mismatch');
      return;
    }
    if (needsSetup && password.length < 8) {
      error = t('login.tooShort');
      return;
    }

    pending = true;
    try {
      if (needsSetup) {
        await api.auth.setup(password);
      } else {
        await api.auth.login(password);
      }
      onauthenticated();
    } catch (err) {
      error = err.message;
      password = '';
      confirm = '';
    } finally {
      pending = false;
    }
  }
</script>

<div class="page">
  <!-- The topbar is not rendered yet, and someone setting up a server in a
       language their browser did not advertise should not have to sign in
       first to switch it. -->
  <div class="corner"><Appearance /></div>

  <form onsubmit={submit}>
    <div class="mark">
      <svg viewBox="0 0 100 100" width="34" height="34" aria-hidden="true">
        <rect width="100" height="100" rx="22" fill="currentColor" opacity="0.1" />
        <path
          d="M32 74V26h20a16 16 0 0 1 0 32H42"
          stroke="currentColor"
          stroke-width="9"
          fill="none"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
    </div>

    <h1>{needsSetup ? t('login.setupTitle') : t('login.signInTitle')}</h1>
    <p class="muted">{needsSetup ? t('login.setupBlurb') : t('login.signInBlurb')}</p>

    <label for="pw">{t('login.password')}</label>
    <input
      id="pw"
      type="password"
      bind:value={password}
      autocomplete={needsSetup ? 'new-password' : 'current-password'}
      required
    />

    {#if needsSetup}
      <label for="pw2">{t('login.confirmPassword')}</label>
      <input id="pw2" type="password" bind:value={confirm} autocomplete="new-password" required />
    {/if}

    {#if error}<p class="error">{error}</p>{/if}

    <Button type="submit" variant="primary" size="lg" {pending}>
      {needsSetup ? t('login.create') : t('login.signIn')}
    </Button>
  </form>
</div>

<style>
  .page {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 24px;
  }

  form {
    width: 100%;
    max-width: 360px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 28px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    box-shadow: var(--shadow);
  }

  .corner {
    position: fixed;
    top: 12px;
    right: 12px;
  }

  .mark { color: var(--accent); margin-bottom: 6px; }

  h1 { font-size: 19px; }

  form p.muted {
    margin: 0 0 12px;
    font-size: 13px;
    line-height: 1.5;
  }

  label {
    margin-top: 8px;
    font-size: 12.5px;
    font-weight: 550;
  }

  input {
    padding: 8px 10px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 14px;
  }
  input:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  .error {
    margin: 8px 0 0;
    font-size: 12.5px;
    color: var(--bad);
  }

  form :global(.btn) { margin-top: 16px; }
</style>
