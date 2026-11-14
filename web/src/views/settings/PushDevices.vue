<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { usePushNotifications } from '@/composables/usePushNotifications'
import { useToast } from '@/composables/useToast'
import { useFormatters } from '@/composables/useFormatters'

const push = usePushNotifications()
const { show: showToast } = useToast()
const { formatRel } = useFormatters()
const loaded = ref(false)
const testing = ref<string | null>(null)

onMounted(async () => {
  try {
    await push.refresh()
  } catch {
    /* the section explains push is unavailable */
  } finally {
    loaded.value = true
  }
})

const blockerText = computed(() => {
  switch (push.blocker.value) {
    case 'insecure':
      return 'Push notifications need Tindra to be served over HTTPS.'
    case 'ios-install':
      return 'On iPhone and iPad, add Tindra to your Home Screen first (Share, then Add to Home Screen) and open it from there.'
    case 'unsupported':
      return 'This browser does not support push notifications.'
    case 'disabled':
      return 'Push notifications are not available on this server.'
    case 'denied':
      return 'Notifications are blocked for this site. Allow them in your browser settings, then try again.'
    default:
      return ''
  }
})

function errorText(err: unknown, fallback: string) {
  return err instanceof Error && err.message ? err.message : fallback
}

async function toggleHere() {
  try {
    if (push.enabledHere.value) {
      await push.disable()
      showToast('Push notifications turned off on this device')
      return
    }
    const saved = await push.enable()
    if (saved) showToast('Push notifications turned on for this device')
    else if (push.permission.value === 'denied') showToast('Notifications are blocked for this site', 'error')
  } catch (err) {
    showToast(errorText(err, 'Could not change push notifications'), 'error')
  }
}

async function test(id: string) {
  testing.value = id
  try {
    await push.sendTest(id)
    showToast('Test notification sent')
  } catch (err) {
    showToast(errorText(err, 'Test notification failed'), 'error')
    await push.refresh().catch(() => {})
  } finally {
    testing.value = null
  }
}

async function remove(id: string) {
  try {
    await push.remove(id)
    showToast('Device removed')
  } catch (err) {
    showToast(errorText(err, 'Could not remove device'), 'error')
  }
}
</script>

<template>
  <div class="push-devices">
    <label class="toggle-row" :class="{ 'push-devices__row--blocked': push.blocker.value }">
      <span class="toggle-row__label">
        Push notifications on this device
        <span class="toggle-row__hint">
          Alert rules using the Push channel notify you here, even when Tindra is closed.
        </span>
      </span>
      <button
        class="toggle"
        :class="{ 'toggle--on': push.enabledHere.value }"
        role="switch"
        :aria-checked="push.enabledHere.value"
        :disabled="!loaded || push.busy.value || (!!push.blocker.value && !push.enabledHere.value)"
        @click.prevent="toggleHere"
      >
        <span class="toggle__knob" />
      </button>
    </label>
    <p v-if="loaded && blockerText" class="field__hint push-devices__hint" data-testid="push-blocker">{{ blockerText }}</p>

    <ul v-if="push.devices.value.length" class="push-devices__list" aria-label="Registered devices">
      <li v-for="d in push.devices.value" :key="d.id" class="push-devices__item">
        <div class="push-devices__meta">
          <span class="push-devices__name">
            {{ d.label || 'Unnamed device' }}
            <span v-if="d.endpoint === push.currentEndpoint.value" class="push-devices__badge">This device</span>
          </span>
          <span class="push-devices__sub">
            Added {{ formatRel(d.created_at) }}<template v-if="d.last_success_at"> · last delivered {{ formatRel(d.last_success_at) }}</template><template v-if="d.failure_count > 0"> · {{ d.failure_count }} failed {{ d.failure_count === 1 ? 'delivery' : 'deliveries' }}</template>
          </span>
        </div>
        <div class="push-devices__actions">
          <button class="btn btn--ghost push-devices__btn" :disabled="testing === d.id" @click="test(d.id)">
            {{ testing === d.id ? 'Sending…' : 'Send test' }}
          </button>
          <button class="btn btn--ghost push-devices__btn" :aria-label="`Remove ${d.label || 'device'}`" @click="remove(d.id)">Remove</button>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.push-devices {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 480px;
  margin-top: 18px;
}
.push-devices__row--blocked .toggle-row__label { color: var(--text-2); }
.push-devices__hint { margin: 0; }
.push-devices__list {
  list-style: none;
  margin: 4px 0 0;
  padding: 0;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
}
.push-devices__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
}
.push-devices__item + .push-devices__item { border-top: 1px solid var(--border-soft); }
.push-devices__meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.push-devices__name {
  font-size: var(--text-sm);
  color: var(--text-1);
  display: flex;
  align-items: center;
  gap: 6px;
}
.push-devices__badge {
  font-size: var(--text-xs);
  color: var(--accent);
}
.push-devices__sub {
  font-size: var(--text-xs);
  color: var(--text-3);
}
.push-devices__actions {
  display: flex;
  gap: 4px;
  flex: 0 0 auto;
}
.push-devices__btn {
  height: 28px;
  padding: 0 8px;
  font-size: var(--text-xs);
}
@media (max-width: 480px) {
  .push-devices__item { flex-direction: column; align-items: flex-start; }
}
</style>
