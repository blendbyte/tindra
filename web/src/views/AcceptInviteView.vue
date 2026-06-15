<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { apiFetch } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/Icon.vue'
import logoLight from '@/assets/logo.png'
import logoDark from '@/assets/logo-dark.png'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
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
    auth.ready = false
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
