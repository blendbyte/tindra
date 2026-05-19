<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import Icon from '@/components/Icon.vue'

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
      <svg
        xmlns="http://www.w3.org/2000/svg"
        viewBox="0 0 416 144"
        preserveAspectRatio="xMidYMid meet"
        class="login__logo-svg"
        aria-label="Tindra"
      >
        <defs>
          <linearGradient id="ll-top" x1="120" y1="20" x2="55" y2="80" gradientUnits="userSpaceOnUse">
            <stop offset="0" stop-color="#f4f7f7"/>
            <stop offset="0.5" stop-color="#e6ecec"/>
            <stop offset="1" stop-color="#d6dddd"/>
          </linearGradient>
          <linearGradient id="ll-right" x1="115" y1="18" x2="115" y2="95" gradientUnits="userSpaceOnUse">
            <stop offset="0" stop-color="#f4f2fc"/>
            <stop offset="0.30" stop-color="#dad7f4"/>
            <stop offset="0.60" stop-color="#acaeea"/>
            <stop offset="0.85" stop-color="#8087d4"/>
            <stop offset="1" stop-color="#6a72c5"/>
          </linearGradient>
          <linearGradient id="ll-bottom" x1="28" y1="101" x2="110" y2="115" gradientUnits="userSpaceOnUse">
            <stop offset="0" stop-color="#4a4a7d"/>
            <stop offset="0.25" stop-color="#6c66a3"/>
            <stop offset="0.55" stop-color="#a89ed4"/>
            <stop offset="0.80" stop-color="#d6cef0"/>
            <stop offset="1" stop-color="#ebe6f8"/>
          </linearGradient>
          <linearGradient id="ll-hull" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stop-color="#1f2638"/>
            <stop offset="1" stop-color="#161b29"/>
          </linearGradient>
        </defs>
        <polygon points="133,4 127,120 8,96" fill="url(#ll-hull)" stroke="url(#ll-hull)" stroke-width="6" stroke-linejoin="round" stroke-linecap="round"/>
        <polygon points="130,7 99,74 46,84" fill="url(#ll-top)" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round"/>
        <polygon points="130,7 99,74 115,95" fill="url(#ll-right)" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round"/>
        <polygon points="99,74 115,95 123,117 68,81" fill="#1a2033" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round"/>
        <polygon points="68,81 123,117 28,101 46,84" fill="url(#ll-bottom)" stroke="#0e1320" stroke-width="0.35" stroke-linejoin="round"/>
        <path d="M 46 84 L 99 74" stroke="#ffffff" stroke-opacity="0.25" stroke-width="0.5" fill="none"/>
        <path d="M 99 74 L 130 7" stroke="#ffffff" stroke-opacity="0.18" stroke-width="0.5" fill="none"/>
        <text
          x="158" y="103"
          font-family="Manrope, Helvetica Neue, Arial Black, Arial, sans-serif"
          font-size="92"
          font-weight="800"
          textLength="248"
          lengthAdjust="spacingAndGlyphs"
          letter-spacing="-2"
          fill="currentColor"
        >Tindra</text>
      </svg>

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
