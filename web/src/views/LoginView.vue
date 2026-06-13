<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import Icon from '@/components/Icon.vue'
import logoLight from '@/assets/logo.png'
import logoDark from '@/assets/logo-dark.png'

const router = useRouter()
const qc = useQueryClient()
const email = ref('')
const password = ref('')
const error = ref<string | null>(null)
const loading = ref(false)

// MFA step
const mfaToken = ref<string | null>(null)
const mfaCode = ref('')
const mfaLoading = ref(false)

const { data: providersData } = useQuery({
  queryKey: ['auth-providers'],
  queryFn: () => apiFetch<{ providers: string[] }>('/api/auth/providers'),
  staleTime: Infinity,
})

const providers = computed(() => providersData.value?.providers ?? [])
const hasSso = computed(() => providers.value.length > 0)

function providerLabel(name: string) {
  const labels: Record<string, string> = {
    google: 'Google Workspace',
    github: 'GitHub',
    microsoft: 'Microsoft',
    zitadel: 'Zitadel',
    auth0: 'Auth0',
  }
  return labels[name] ?? name.charAt(0).toUpperCase() + name.slice(1)
}

interface LoginError {
  title: string
  hint: string
}

function mapError(raw: string): LoginError {
  const m = raw.toLowerCase()
  if (m.includes('required')) return { title: 'Email and password are required.', hint: '' }
  if (m.includes('credentials') || m.includes('unauthorized') || m.includes('invalid credentials'))
    return { title: 'Incorrect email or password.', hint: 'Contact your administrator if you\'ve lost access to your account.' }
  if (m.includes('disabled') || m.includes('forbidden'))
    return { title: 'Password login is disabled.', hint: 'Use the SSO provider configured for this instance.' }
  if (m.includes('code') || m.includes('invalid code'))
    return { title: 'That code didn\'t work.', hint: 'Open your authenticator app for a fresh 6-digit code. Codes expire every 30 seconds.' }
  if (m.includes('expired'))
    return { title: 'Session expired.', hint: 'Sign in again to continue.' }
  return { title: raw, hint: '' }
}

const loginError = computed<LoginError | null>(() => error.value ? mapError(error.value) : null)

async function submit() {
  if (!email.value || !password.value) {
    error.value = 'required'
    return
  }
  error.value = null
  loading.value = true
  try {
    const res = await apiFetch<{ mfa_required?: boolean; mfa_token?: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email: email.value, password: password.value }),
    })
    if (res?.mfa_required && res.mfa_token) {
      mfaToken.value = res.mfa_token
      mfaCode.value = ''
      error.value = null
    } else {
      await qc.resetQueries()
      router.push('/dashboard')
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'invalid credentials'
  } finally {
    loading.value = false
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
    await qc.resetQueries()
    router.push('/dashboard')
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'invalid code'
    mfaCode.value = ''
  } finally {
    mfaLoading.value = false
  }
}

// Auto-submit when 6 digits are entered
watch(mfaCode, (v) => {
  if (v.length === 6) submitMFA()
})

function backToLogin() {
  mfaToken.value = null
  mfaCode.value = ''
  error.value = null
}
</script>

<template>
  <div class="login">
    <div class="login__card">
      <!-- Logo -->
      <img :src="logoLight" alt="Tindra" class="login__logo login__logo--light" />
      <img :src="logoDark" alt="Tindra" class="login__logo login__logo--dark" />

      <!-- MFA step -->
      <template v-if="mfaToken">
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
          <div v-if="loginError" class="login__error-box">
            <Icon name="alert-circle" :size="14" class="login__error-icon" />
            <div>
              <div class="login__error-title">{{ loginError.title }}</div>
              <div v-if="loginError.hint" class="login__error-hint">{{ loginError.hint }}</div>
            </div>
          </div>
          <button
            class="btn btn--primary login__submit"
            :disabled="mfaCode.length < 6 || mfaLoading"
            @click="submitMFA"
          >
            {{ mfaLoading ? 'Verifying…' : 'Verify' }}
            <span class="btn__kbd">↵</span>
          </button>
          <button class="login__back" @click="backToLogin">
            ← Back to sign in
          </button>
        </div>
      </template>

      <!-- SSO-only -->
      <template v-else-if="hasSso">
        <a
          v-for="p in providers"
          :key="p"
          :href="`/api/auth/${p}/redirect`"
          class="btn login__sso"
        >
          <Icon name="shield" :size="14" />
          Continue with {{ providerLabel(p) }}
        </a>
      </template>

      <!-- Password login -->
      <template v-else>
        <form class="login__form" novalidate @submit.prevent="submit">
          <div class="field">
            <label class="field__label" for="email">Email</label>
            <input
              id="email"
              v-model="email"
              type="email"
              class="field__input"
              placeholder="you@example.com"
              autocomplete="email"
              autofocus
            />
          </div>

          <div class="field">
            <label class="field__label" for="password">Password</label>
            <input
              id="password"
              v-model="password"
              type="password"
              class="field__input"
              placeholder="••••••••••••"
              autocomplete="current-password"
            />
          </div>

          <div v-if="loginError" class="login__error-box">
            <Icon name="alert-circle" :size="14" class="login__error-icon" />
            <div>
              <div class="login__error-title">{{ loginError.title }}</div>
              <div v-if="loginError.hint" class="login__error-hint">{{ loginError.hint }}</div>
            </div>
          </div>

          <button type="submit" class="btn btn--primary login__submit" :disabled="loading">
            Sign in
            <span class="btn__kbd">↵</span>
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
