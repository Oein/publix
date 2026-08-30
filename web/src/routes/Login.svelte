<script>
  import { api } from '../lib/api.js';
  import Button from '../lib/Button.svelte';

  let { needsSetup, onauthenticated } = $props();

  let password = $state('');
  let confirm = $state('');
  let pending = $state(false);
  let error = $state('');

  async function submit(event) {
    event.preventDefault();
    error = '';

    if (needsSetup && password !== confirm) {
      error = 'The two passwords do not match.';
      return;
    }
    if (needsSetup && password.length < 8) {
      error = 'Choose a password of at least 8 characters.';
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

    <h1>{needsSetup ? 'Set up publix' : 'Sign in to publix'}</h1>
    <p class="muted">
      {needsSetup
        ? 'Choose a password for this server. It is the only credential that protects your deployments, so make it a real one.'
        : 'Enter the admin password for this server.'}
    </p>

    <label for="pw">Password</label>
    <input
      id="pw"
      type="password"
      bind:value={password}
      autocomplete={needsSetup ? 'new-password' : 'current-password'}
      required
    />

    {#if needsSetup}
      <label for="pw2">Confirm password</label>
      <input id="pw2" type="password" bind:value={confirm} autocomplete="new-password" required />
    {/if}

    {#if error}<p class="error">{error}</p>{/if}

    <Button type="submit" variant="primary" size="lg" {pending}>
      {needsSetup ? 'Create password' : 'Sign in'}
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
