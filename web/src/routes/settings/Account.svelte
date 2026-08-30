<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';

  let current = $state('');
  let next = $state('');
  let confirm = $state('');
  let saving = $state(false);

  async function change() {
    if (next !== confirm) {
      notify.error('The two new passwords do not match.');
      return;
    }
    if (next.length < 8) {
      notify.error('Choose a password of at least 8 characters.');
      return;
    }
    saving = true;
    try {
      await api.auth.changePassword(current, next);
      // Changing the password rotates the signing key, so every session
      // ends — including this one. That is the point.
      notify.success('Password changed. Signing you in again…');
      window.location.reload();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

<Card title="Change password">
  <div class="form">
    <Field label="Current password" required>
      {#snippet children(id)}
        <input {id} type="password" bind:value={current} autocomplete="current-password" />
      {/snippet}
    </Field>
    <Field label="New password" hint="At least 8 characters." required>
      {#snippet children(id)}
        <input {id} type="password" bind:value={next} autocomplete="new-password" />
      {/snippet}
    </Field>
    <Field label="Confirm new password" required>
      {#snippet children(id)}
        <input {id} type="password" bind:value={confirm} autocomplete="new-password" />
      {/snippet}
    </Field>

    <p class="muted small">
      Changing the password signs out every session on every device, including this one.
    </p>

    <div>
      <Button variant="primary" pending={saving} disabled={!current || !next} onclick={change}>
        Change password
      </Button>
    </div>
  </div>
</Card>

<style>
  .form { display: flex; flex-direction: column; gap: 14px; max-width: 420px; }
</style>
