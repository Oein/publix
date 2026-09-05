<script>
  import { api } from '../../lib/api.js';
  import { notify } from '../../lib/toast.js';
  import Button from '../../lib/Button.svelte';
  import Card from '../../lib/Card.svelte';
  import Field from '../../lib/Field.svelte';
  import { t } from '../../lib/i18n.svelte.js';

  let current = $state('');
  let next = $state('');
  let confirm = $state('');
  let saving = $state(false);

  async function change() {
    if (next !== confirm) {
      notify.error(t('account.mismatch'));
      return;
    }
    if (next.length < 8) {
      notify.error(t('account.tooShort'));
      return;
    }
    saving = true;
    try {
      await api.auth.changePassword(current, next);
      // Changing the password rotates the signing key, so every session
      // ends — including this one. That is the point.
      notify.success(t('account.changed'));
      window.location.reload();
    } catch (err) {
      notify.error(err);
    } finally {
      saving = false;
    }
  }
</script>

<Card title={t('account.title')}>
  <div class="form">
    <Field label={t('account.current')} required>
      {#snippet children(id)}
        <input {id} type="password" bind:value={current} autocomplete="current-password" />
      {/snippet}
    </Field>
    <Field label={t('account.new')} hint={t('account.newHint')} required>
      {#snippet children(id)}
        <input {id} type="password" bind:value={next} autocomplete="new-password" />
      {/snippet}
    </Field>
    <Field label={t('account.confirm')} required>
      {#snippet children(id)}
        <input {id} type="password" bind:value={confirm} autocomplete="new-password" />
      {/snippet}
    </Field>

    <p class="muted small">{t('account.blurb')}</p>

    <div>
      <Button variant="primary" pending={saving} disabled={!current || !next} onclick={change}>
        {t('account.change')}
      </Button>
    </div>
  </div>
</Card>

<style>
  .form { display: flex; flex-direction: column; gap: 14px; max-width: 420px; }
</style>
