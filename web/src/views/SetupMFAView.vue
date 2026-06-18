<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/Icon.vue'
import logoLight from '@/assets/logo.png'
import logoDark from '@/assets/logo-dark.png'

const router = useRouter()
const qc = useQueryClient()
const auth = useAuthStore()

const setupData = ref<{ secret: string; uri: string; qr: string } | null>(null)
const loadError = ref<string | null>(null)
const showSecret = ref(false)
const code = ref('')
const confirmError = ref<string | null>(null)
const confirming = ref(false)
const done = ref(false)

onMounted(async () => {
  try {
    setupData.value = await apiFetch<{ secret: string; uri: string; qr: string }>('/api/auth/mfa/setup')
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Failed to load setup'
  }
})

watch(code, (v) => {
  if (v.length === 6) submit()
})

async function submit() {
  if (code.value.length < 6 || confirming.value) return
  confirmError.value = null
  confirming.value = true
  try {
    await apiFetch('/api/auth/mfa/confirm', {
      method: 'POST',
      body: JSON.stringify({ code: code.value }),
    })
    done.value = true
    auth.ready = false
    await qc.resetQueries()
    setTimeout(() => router.push('/dashboard'), 1400)
  } catch (e) {
    confirmError.value = e instanceof Error ? e.message : "That code didn't work. Try again."
    code.value = ''
    confirming.value = false
  }
}

function copySecret() {
  if (setupData.value) {
    navigator.clipboard?.writeText(setupData.value.secret).catch(() => {})
  }
}
</script>

<template>
  <div class="login">
    <div class="login__card mfa-gate-card">
      <img :src="logoLight" alt="Tindra" class="login__logo login__logo--light" />
      <img :src="logoDark" alt="Tindra" class="login__logo login__logo--dark" />

      <!-- Success state -->
      <template v-if="done">
        <div class="mfa-gate__success">
          <div class="mfa-gate__success-icon">
            <Icon name="shield-check" :size="26" />
          </div>
          <div class="mfa-gate__success-title">You're protected</div>
          <div class="mfa-gate__hint" style="text-align: center">
            Two-factor authentication is now active on your account.
          </div>
        </div>
      </template>

      <!-- Setup flow -->
      <template v-else-if="setupData">
        <div class="mfa-gate">
          <div class="mfa-gate__header">
            <div class="mfa-gate__badge">
              <Icon name="shield" :size="18" />
            </div>
            <div>
              <div class="mfa-gate__title">Two-factor authentication required</div>
              <div class="mfa-gate__hint">
                Secure your account with an authenticator app before continuing.
              </div>
            </div>
          </div>

          <div class="mfa-gate__divider" />

          <div class="mfa-gate__step">
            <span class="mfa-setup-card__num">1</span>
            <span>Scan this QR code with your authenticator app (Google Authenticator, Authy, 1Password…).</span>
          </div>

          <div class="mfa-gate__qr">
            <img :src="setupData.qr" alt="TOTP QR code" width="160" height="160" class="mfa-qr__img" />
          </div>

          <button class="mfa-secret-toggle" style="margin-left: 0" @click="showSecret = !showSecret">
            <Icon name="chevron-right" :size="11" :style="showSecret ? 'transform:rotate(90deg)' : ''" />
            Can't scan? Enter the key manually
          </button>

          <div v-if="showSecret" class="mfa-gate__secret-row">
            <code class="mfa-setup-card__secret" style="flex: 1">{{ setupData.secret }}</code>
            <button
              class="btn btn--ghost"
              style="height: 28px; padding: 0 8px; font-size: var(--text-xs); flex-shrink: 0"
              @click="copySecret"
            >
              <Icon name="copy" :size="11" /> Copy
            </button>
          </div>

          <div class="mfa-gate__step" style="margin-top: 20px">
            <span class="mfa-setup-card__num">2</span>
            <span>Enter the 6-digit code from your app to confirm and activate.</span>
          </div>

          <div class="mfa-gate__code-row">
            <input
              v-model="code"
              class="field__input login__mfa-code mfa-gate__code-input"
              placeholder="000000"
              maxlength="6"
              inputmode="numeric"
              autocomplete="one-time-code"
              autofocus
            />
            <button
              class="btn btn--primary"
              :disabled="code.length < 6 || confirming"
              @click="submit"
            >
              {{ confirming ? 'Verifying…' : 'Confirm' }}
            </button>
          </div>

          <div v-if="confirmError" class="login__error-box" style="margin-top: 12px">
            <Icon name="alert-circle" :size="14" class="login__error-icon" />
            <div class="login__error-title">{{ confirmError }}</div>
          </div>
        </div>
      </template>

      <!-- Loading -->
      <template v-else-if="!loadError">
        <div class="mfa-gate__loading">
          <div class="mfa-gate__badge">
            <Icon name="shield" :size="18" />
          </div>
          <div class="mfa-gate__title" style="text-align: center">Preparing setup…</div>
        </div>
      </template>

      <!-- Load error -->
      <template v-else>
        <div class="login__error-box" style="width: 100%">
          <Icon name="alert-circle" :size="14" class="login__error-icon" />
          <div>
            <div class="login__error-title">Could not start setup</div>
            <div class="login__error-hint">{{ loadError }}</div>
          </div>
        </div>
      </template>

    </div>
  </div>
</template>
