<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/Icon.vue'
import logoLight from '@/assets/logo.png'
import logoDark from '@/assets/logo-dark.png'
import { useClipboard } from '@/composables/useClipboard'
import { useToast } from '@/composables/useToast'

const router = useRouter()
const qc = useQueryClient()
const auth = useAuthStore()
const { show: showToast } = useToast()

const setupData = ref<{ secret: string; uri: string; qr: string } | null>(null)
const loadError = ref<string | null>(null)
const showSecret = ref(false)
const code = ref('')
const codeInput = ref<HTMLInputElement | null>(null)
watch(codeInput, input => input?.focus(), { flush: 'post' })
const confirmError = ref<string | null>(null)
const confirming = ref(false)
// Setup right after signing in needs nothing more. A session signed in a while
// ago confirms the password first (400), or signs in again for SSO accounts.
const needsPassword = ref(false)
const password = ref('')
const starting = ref(false)

async function startSetup() {
  starting.value = true
  try {
    setupData.value = await apiFetch<{ secret: string; uri: string; qr: string }>('/api/auth/mfa/setup', {
      method: 'POST',
      ...(password.value ? { body: JSON.stringify({ password: password.value }) } : {}),
    })
    loadError.value = null
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Failed to load setup'
    if ((e as { status?: number }).status === 400) needsPassword.value = true
  } finally {
    password.value = ''
    starting.value = false
  }
}

onMounted(startSetup)

async function signInAgain() {
  await apiFetch('/api/auth/logout', { method: 'POST' }).catch(() => {})
  window.location.href = '/login'
}

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
    await qc.resetQueries()
    await auth.refresh()
    showToast('Two-factor authentication enabled')
    await router.push('/dashboard')
  } catch (e) {
    confirmError.value = e instanceof Error ? e.message : "That code didn't work. Try again."
    code.value = ''
    confirming.value = false
  }
}

const { copy } = useClipboard()

function copySecret() {
  if (setupData.value) {
    copy(setupData.value.secret, 'Secret copied')
  }
}
</script>

<template>
  <div class="login">
    <div class="login__card mfa-gate-card">
      <img :src="logoLight" alt="Tindra" class="login__logo login__logo--light" />
      <img :src="logoDark" alt="Tindra" class="login__logo login__logo--dark" />

      <!-- Setup flow -->
      <template v-if="setupData">
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
            <span id="setup-mfa-hint">Enter the 6-digit code from your app to confirm and activate.</span>
          </div>

          <label for="setup-mfa-code" class="field__label">Authenticator code</label>
          <div class="mfa-gate__code-row">
            <input
              id="setup-mfa-code"
              ref="codeInput"
              v-model="code"
              class="field__input login__mfa-code mfa-gate__code-input"
              placeholder="000000"
              maxlength="6"
              inputmode="numeric"
              autocomplete="one-time-code"
              :aria-invalid="!!confirmError || undefined"
              :aria-describedby="confirmError ? 'setup-mfa-hint setup-mfa-error' : 'setup-mfa-hint'"
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

          <div v-if="confirmError" id="setup-mfa-error" role="alert" class="login__error-box" style="margin-top: 12px">
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
        <form v-if="needsPassword" class="mfa-gate" style="width: 100%" @submit.prevent="startSetup">
          <div class="mfa-gate__title">Confirm it's you</div>
          <div class="mfa-gate__hint">Enter your password to set up two-factor authentication.</div>
          <label for="setup-mfa-password" class="field__label" style="margin-top: 12px">Password</label>
          <input id="setup-mfa-password" v-model="password" class="field__input" type="password" autocomplete="current-password" required
            :aria-describedby="loadError ? 'setup-mfa-load-error' : undefined" />
          <div v-if="loadError" id="setup-mfa-load-error" role="alert" class="login__error-hint" style="margin-top: 8px">{{ loadError }}</div>
          <button class="btn btn--primary" type="submit" :disabled="!password || starting" style="margin-top: 12px; width: 100%">
            {{ starting ? 'Checking…' : 'Continue' }}
          </button>
        </form>
        <div v-else class="login__error-box" style="width: 100%">
          <Icon name="alert-circle" :size="14" class="login__error-icon" />
          <div>
            <div class="login__error-title">Could not start setup</div>
            <div class="login__error-hint">{{ loadError }}</div>
          </div>
        </div>
        <button class="btn btn--ghost" type="button" style="margin-top: 12px" @click="signInAgain">Sign in again</button>
      </template>

    </div>
  </div>
</template>
