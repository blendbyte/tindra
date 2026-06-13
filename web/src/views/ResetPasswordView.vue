<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { apiFetch } from '@/api/client'
import Icon from '@/components/Icon.vue'
import logoLight from '@/assets/logo.png'
import logoDark from '@/assets/logo-dark.png'

const route = useRoute()
const router = useRouter()
const token = route.params.token as string

const state = ref<'loading' | 'ready' | 'submitting' | 'done' | 'invalid'>('loading')
const email = ref('')
const password = ref('')
const confirm = ref('')
const error = ref<string | null>(null)

const mfaToken = ref<string | null>(null)
const mfaCode = ref('')
const mfaLoading = ref(false)

onMounted(async () => {
  try {
    const data = await apiFetch<{ email: string }>(`/api/auth/password-reset/${token}`)
    email.value = data.email
    state.value = 'ready'
  } catch {
    state.value = 'invalid'
  }
})

async function submit(e: Event) {
  e.preventDefault()
  if (state.value === 'submitting') return
  error.value = null
  if (password.value !== confirm.value) {
    error.value = 'Passwords do not match.'
    return
  }
  if (password.value.length < 12) {
    error.value = 'Password must be at least 12 characters.'
    return
  }
  state.value = 'submitting'
  try {
    const res = await apiFetch<{ mfa_required?: boolean; mfa_token?: string }>(
      `/api/auth/password-reset/${token}`,
      { method: 'POST', body: JSON.stringify({ password: password.value }) },
    )
    if (res?.mfa_required && res.mfa_token) {
      mfaToken.value = res.mfa_token
      mfaCode.value = ''
      state.value = 'ready'
    } else {
      await router.push('/issues')
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Something went wrong. Try again.'
    state.value = 'ready'
  }
}

async function submitMFA() {
  if (mfaCode.value.length < 6 || !mfaToken.value || mfaLoading.value) return
  error.value = null
  mfaLoading.value = true
  try {
    await apiFetch<void>('/api/auth/mfa/verify', {
      method: 'POST',
      body: JSON.stringify({ mfa_token: mfaToken.value, code: mfaCode.value }),
    })
    await router.push('/issues')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Invalid code.'
    mfaCode.value = ''
  } finally {
    mfaLoading.value = false
  }
}

watch(mfaCode, (v) => {
  if (v.length === 6) submitMFA()
})
</script>

<template>
  <div class="login">
    <div class="login__card">
      <!-- Logo -->
      <img :src="logoLight" alt="Tindra" class="login__logo login__logo--light" />
      <img :src="logoDark" alt="Tindra" class="login__logo login__logo--dark" />

      <template v-if="state === 'loading'">
        <div class="login__invite-loading">
          <div class="skeleton" style="width: 180px; height: 14px; border-radius: 4px" />
          <div class="skeleton" style="width: 120px; height: 14px; border-radius: 4px; margin-top: 6px" />
        </div>
      </template>

      <template v-else-if="state === 'invalid'">
        <div class="login__error-box" style="margin-top: 8px">
          <Icon name="alert-circle" :size="14" class="login__error-icon" />
          <div>
            <div class="login__error-title">This reset link has expired or was already used.</div>
            <div class="login__error-hint">Ask your administrator to send a new one.</div>
          </div>
        </div>
        <a href="/login" class="btn btn--primary login__submit" style="text-align: center; text-decoration: none">
          Go to sign in
        </a>
      </template>

      <!-- MFA step -->
      <template v-else-if="mfaToken">
        <div class="login__mfa">
          <div class="login__mfa-icon">
            <Icon name="shield" :size="22" />
          </div>
          <div class="login__mfa-title">Two-factor authentication</div>
          <div class="login__mfa-hint">Enter the 6-digit code from your authenticator app.</div>
          <input
            v-model="mfaCode"
            class="field__input login__mfa-code"
            placeholder="000000"
            maxlength="6"
            inputmode="numeric"
            autocomplete="one-time-code"
            autofocus
          />
          <div v-if="error" class="login__error-box">
            <Icon name="alert-circle" :size="14" class="login__error-icon" />
            <div><div class="login__error-title">{{ error }}</div></div>
          </div>
          <button
            class="btn btn--primary login__submit"
            :disabled="mfaCode.length < 6 || mfaLoading"
            @click="submitMFA"
          >
            {{ mfaLoading ? 'Verifying…' : 'Verify' }}
            <span class="btn__kbd">↵</span>
          </button>
        </div>
      </template>

      <template v-else>
        <div class="login__invite-header">
          <div class="login__invite-title">Set a new password</div>
          <div class="login__invite-email">{{ email }}</div>
        </div>

        <form class="login__form" style="margin-top: 24px" novalidate @submit="submit">
          <div class="field">
            <label class="field__label" for="reset-password">New password <span class="muted">(min 12 characters)</span></label>
            <input
              id="reset-password"
              v-model="password"
              type="password"
              class="field__input"
              placeholder="New password"
              autocomplete="new-password"
              autofocus
            />
          </div>

          <div class="field">
            <label class="field__label" for="reset-confirm">Confirm password</label>
            <input
              id="reset-confirm"
              v-model="confirm"
              type="password"
              class="field__input"
              placeholder="Confirm password"
              autocomplete="new-password"
            />
          </div>

          <div v-if="error" class="login__error-box">
            <Icon name="alert-circle" :size="14" class="login__error-icon" />
            <div>
              <div class="login__error-title">{{ error }}</div>
            </div>
          </div>

          <button
            type="submit"
            class="btn btn--primary login__submit"
            :disabled="!password || !confirm || state === 'submitting'"
          >
            {{ state === 'submitting' ? 'Saving…' : 'Set password' }}
            <span v-if="state !== 'submitting'" class="btn__kbd">↵</span>
          </button>
        </form>
      </template>

      <div class="login__hint">
        <a href="https://tindra.sh" target="_blank" rel="noopener" class="login__hint-link">
          <Icon name="globe" :size="11" />
          tindra.sh
        </a>
        <span aria-hidden="true">·</span>
        <a href="https://github.com/blendbyte/tindra" target="_blank" rel="noopener" class="login__hint-link">
          <Icon name="github" :size="11" />
          GitHub
        </a>
      </div>
    </div>
  </div>
</template>
