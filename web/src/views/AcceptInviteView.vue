<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { apiFetch } from '@/api/client'
import Icon from '@/components/Icon.vue'

const route = useRoute()
const router = useRouter()
const token = route.params.token as string

const state = ref<'loading' | 'ready' | 'submitting' | 'error' | 'invalid'>('loading')
const inviteEmail = ref('')
const inviteName = ref('')
const name = ref('')
const password = ref('')
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    const data = await apiFetch<{ email: string; name: string }>(`/api/auth/invite/${token}`)
    inviteEmail.value = data.email
    inviteName.value = data.name ?? ''
    name.value = data.name ?? ''
    state.value = 'ready'
  } catch {
    state.value = 'invalid'
  }
})

async function submit(e: Event) {
  e.preventDefault()
  if (!password.value || state.value === 'submitting') return
  error.value = null
  state.value = 'submitting'
  try {
    await apiFetch(`/api/auth/invite/${token}/accept`, {
      method: 'POST',
      body: JSON.stringify({ password: password.value, name: name.value.trim() }),
    })
    await router.push('/issues')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Something went wrong. Try again.'
    state.value = 'ready'
  }
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
            <div class="login__error-title">This invitation has expired or was already used.</div>
            <div class="login__error-hint">Ask your administrator for a new invite link.</div>
          </div>
        </div>
        <a href="/login" class="btn btn--primary login__submit" style="text-align: center; text-decoration: none">
          Go to sign in
        </a>
      </template>

      <template v-else>
        <div class="login__invite-header">
          <div class="login__invite-title">You've been invited</div>
          <div class="login__invite-email">{{ inviteEmail }}</div>
        </div>

        <form class="login__form" novalidate @submit="submit">
          <div class="field">
            <label class="field__label" for="invite-name">Name <span class="muted">(optional)</span></label>
            <input
              id="invite-name"
              v-model="name"
              type="text"
              class="field__input"
              placeholder="Your name"
              autocomplete="name"
              autofocus
            />
          </div>

          <div class="field">
            <label class="field__label" for="invite-password">Password <span class="muted">(min 12 characters)</span></label>
            <input
              id="invite-password"
              v-model="password"
              type="password"
              class="field__input"
              placeholder="Choose a password"
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
            :disabled="!password || state === 'submitting'"
          >
            {{ state === 'submitting' ? 'Creating account…' : 'Create account' }}
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
