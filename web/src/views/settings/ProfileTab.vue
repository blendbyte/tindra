<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { useClipboard } from '@/composables/useClipboard'
import { apiFetch } from '@/api/client'
import type { User } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { useMe } from '@/composables/useMe'

const auth = useAuthStore()
const { show: showToast } = useToast()
const { copy } = useClipboard()
const qc = useQueryClient()
const { data: me } = useMe()

const allTimezones: string[] = (Intl as any).supportedValuesOf?.('timeZone') ?? ['UTC']

const profileName = ref('')
const profileEmail = ref('')
const profileWeeklyDigest = ref(true)
const profileTimezone = ref('UTC')

function initProfile() {
  profileName.value = me.value?.name ?? ''
  profileEmail.value = me.value?.email ?? ''
  profileWeeklyDigest.value = me.value?.weekly_digest ?? true
  profileTimezone.value = me.value?.timezone ?? 'UTC'
}

// Initialize profile fields whenever me loads. The settings nav also calls
// initProfile when the Profile tab is clicked, which discards unsaved edits.
watch(me, () => {
  if (me.value) initProfile()
}, { immediate: true })

defineExpose({ initProfile })

// Changing the email needs the current password, or a 2FA code for SSO-only accounts.
const profileCurrentPassword = ref('')
const profileMfaCode = ref('')
const emailChanged = computed(() =>
  !!me.value && profileEmail.value.trim().toLowerCase() !== me.value.email.toLowerCase(),
)

const { mutate: updateProfile } = useMutation({
  mutationFn: ({ name, email, weekly_digest, timezone }: { name: string; email: string; weekly_digest: boolean; timezone: string }) =>
    apiFetch<User>('/api/me', {
      method: 'PATCH',
      body: JSON.stringify({
        name, email, weekly_digest, timezone,
        ...(emailChanged.value ? { current_password: profileCurrentPassword.value, mfa_code: profileMfaCode.value } : {}),
      }),
    }),
  onSuccess: (updated) => {
    auth.setUser(updated)
    qc.invalidateQueries({ queryKey: ['me'] })
    profileCurrentPassword.value = ''
    profileMfaCode.value = ''
    showToast('Profile saved')
  },
  onError: (err) => showToast(err instanceof Error ? err.message : 'Failed to save profile.', 'error'),
})

// Password change
const pwCurrent = ref('')
const pwNew = ref('')
const pwConfirm = ref('')
const pwError = ref<string | null>(null)

const { mutate: changePassword, isPending: changingPw } = useMutation({
  mutationFn: ({ current_password, new_password }: { current_password: string; new_password: string }) =>
    apiFetch('/api/me/password', { method: 'PATCH', body: JSON.stringify({ current_password, new_password }) }),
  onSuccess: () => {
    pwCurrent.value = ''
    pwNew.value = ''
    pwConfirm.value = ''
    pwError.value = null
    showToast('Password changed')
  },
  onError: (e) => { pwError.value = e instanceof Error ? e.message : 'Failed to change password' },
})

function submitPasswordChange() {
  pwError.value = null
  if (pwNew.value !== pwConfirm.value) { pwError.value = 'Passwords do not match'; return }
  if (pwNew.value.length < 12) { pwError.value = 'Password must be at least 12 characters'; return }
  changePassword({ current_password: pwCurrent.value, new_password: pwNew.value })
}

// MFA setup / disable
const mfaSetupData = ref<{ secret: string; uri: string; qr: string } | null>(null)
const showMFASecret = ref(false)
const showReplaceMFA = ref(false)
const mfaCurrentCode = ref('')
const mfaCode = ref('')
const mfaCodeInput = ref<HTMLInputElement | null>(null)
watch(mfaCodeInput, input => input?.focus(), { flush: 'post' })
const mfaSetupError = ref<string | null>(null)
const showDisableMFA = ref(false)
const mfaDisablePassword = ref('')
const mfaDisableError = ref<string | null>(null)

const { mutate: startMFASetup, isPending: loadingMFASetup } = useMutation({
  mutationFn: () => apiFetch<{ secret: string; uri: string; qr: string }>('/api/auth/mfa/setup', { method: 'POST', body: JSON.stringify({ code: mfaCurrentCode.value }) }),
  onSuccess: (data) => { mfaSetupData.value = data; mfaCode.value = ''; mfaCurrentCode.value = ''; showReplaceMFA.value = false; mfaSetupError.value = null },
  onError: (e) => { mfaSetupError.value = e instanceof Error ? e.message : 'Failed to start authenticator setup' },
})

const { mutate: confirmMFASetup, isPending: confirmingMFA } = useMutation({
  mutationFn: (code: string) =>
    apiFetch('/api/auth/mfa/confirm', { method: 'POST', body: JSON.stringify({ code }) }),
  onSuccess: () => {
    mfaSetupData.value = null
    mfaCode.value = ''
    mfaSetupError.value = null
    qc.invalidateQueries({ queryKey: ['me'] })
    showToast('Two-factor authentication enabled')
  },
  onError: (e) => { mfaSetupError.value = e instanceof Error ? e.message : 'That code didn\'t work. Try again.' },
})

const { mutate: disableMFA, isPending: disablingMFA } = useMutation({
  mutationFn: (password: string) =>
    apiFetch('/api/auth/mfa', { method: 'DELETE', body: JSON.stringify({ password }) }),
  onSuccess: () => {
    showDisableMFA.value = false
    mfaDisablePassword.value = ''
    mfaDisableError.value = null
    qc.invalidateQueries({ queryKey: ['me'] })
    showToast('Two-factor authentication disabled')
  },
  onError: (e) => { mfaDisableError.value = e instanceof Error ? e.message : 'Invalid password' },
})

function copyToClipboard(text: string, label: string) {
  copy(text, `${label} copied`)
}

function cancelMFASetup() {
  mfaSetupData.value = null
  mfaCode.value = ''
  mfaSetupError.value = null
}
</script>

<template>
  <div class="pane-head">
    <div class="pane-head__title">Profile</div>
  </div>

  <!-- Personal information -->
  <div class="profile-section">
    <div class="profile-section__title">Personal information</div>
    <div class="profile-section__body">
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px; max-width: 480px">
        <div class="field">
          <label class="field__label">Name</label>
          <input v-model="profileName" class="field__input" :placeholder="me?.name || 'Your name'" />
        </div>
        <div class="field">
          <label class="field__label">Email</label>
          <input v-model="profileEmail" class="field__input" type="email" :placeholder="me?.email ?? 'you@example.com'" />
        </div>
        <div v-if="emailChanged && me?.has_password" class="field" style="grid-column: 1 / -1">
          <label class="field__label" for="profile-current-password">Current password</label>
          <input id="profile-current-password" v-model="profileCurrentPassword" class="field__input" type="password" autocomplete="current-password" placeholder="Confirm it's you to change your email" />
        </div>
        <div v-else-if="emailChanged && me?.mfa_enabled" class="field" style="grid-column: 1 / -1">
          <label class="field__label" for="profile-mfa-code">Two-factor code</label>
          <input id="profile-mfa-code" v-model="profileMfaCode" class="field__input" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="Confirm it's you to change your email" />
        </div>
        <p v-else-if="emailChanged" class="field__hint" style="grid-column: 1 / -1">
          Your account has no password or two-factor code to confirm this change. Ask an admin to update your email.
        </p>
        <div class="field" style="grid-column: 1 / -1">
          <label class="field__label">Timezone</label>
          <select v-model="profileTimezone" class="field__input">
            <option v-for="z in allTimezones" :key="z" :value="z">{{ z }}</option>
          </select>
        </div>
        <div style="grid-column: 1 / -1; display: flex; gap: 8px">
          <button class="btn btn--primary" @click="updateProfile({ name: profileName, email: profileEmail, weekly_digest: profileWeeklyDigest, timezone: profileTimezone })">Save changes</button>
          <button class="btn btn--ghost" @click="initProfile">Cancel</button>
        </div>
      </div>
    </div>
  </div>

  <!-- Notifications -->
  <div class="profile-section">
    <div class="profile-section__title">Notifications</div>
    <div class="profile-section__body">
      <label class="toggle-row">
        <span class="toggle-row__label">
          Weekly digest
          <span class="toggle-row__hint">Receive a weekly summary of errors and performance.</span>
        </span>
        <button
          class="toggle"
          :class="{ 'toggle--on': profileWeeklyDigest }"
          role="switch"
          :aria-checked="profileWeeklyDigest"
          @click="profileWeeklyDigest = !profileWeeklyDigest; updateProfile({ name: profileName, email: profileEmail, weekly_digest: profileWeeklyDigest, timezone: profileTimezone })"
        >
          <span class="toggle__knob" />
        </button>
      </label>
    </div>
  </div>

  <!-- Password (only for password-based accounts) -->
  <div v-if="me?.has_password" class="profile-section">
    <div class="profile-section__title">Password</div>
    <div class="profile-section__body">
      <div style="display: flex; flex-direction: column; gap: 12px; max-width: 320px">
        <div class="field">
          <label class="field__label">Current password</label>
          <input v-model="pwCurrent" class="field__input" type="password" autocomplete="current-password" />
        </div>
        <div class="field">
          <label class="field__label">New password</label>
          <input v-model="pwNew" class="field__input" type="password" autocomplete="new-password" placeholder="12+ characters" />
        </div>
        <div class="field">
          <label class="field__label">Confirm new password</label>
          <input v-model="pwConfirm" class="field__input" type="password" autocomplete="new-password" />
        </div>
        <div v-if="pwError" class="profile-error">{{ pwError }}</div>
        <div style="display: flex; gap: 8px">
          <button class="btn btn--primary" :disabled="changingPw || !pwCurrent || !pwNew || !pwConfirm" @click="submitPasswordChange">
            {{ changingPw ? 'Changing…' : 'Change password' }}
          </button>
        </div>
      </div>
    </div>
  </div>

  <!-- Two-factor authentication -->
  <div class="profile-section">
    <div class="profile-section__title">Two-factor authentication</div>
    <div class="profile-section__body">

      <!-- MFA disabled, no setup in progress -->
      <template v-if="!me?.mfa_enabled && !mfaSetupData">
        <div style="display: flex; align-items: center; gap: 16px">
          <div>
            <div style="font-size: var(--text-sm); color: var(--text-2)">
              Add a second layer of security to your account using a TOTP authenticator app.
            </div>
          </div>
        </div>
        <div style="margin-top: 14px">
          <button class="btn btn--primary" :disabled="loadingMFASetup" @click="startMFASetup()">
            {{ loadingMFASetup ? 'Generating…' : 'Enable two-factor auth' }}
          </button>
          <div v-if="mfaSetupError" class="profile-error">{{ mfaSetupError }}</div>
        </div>
      </template>

      <!-- MFA setup in progress -->
      <template v-else-if="mfaSetupData">
        <div class="mfa-setup-card">
          <div class="mfa-setup-card__step">
            <span class="mfa-setup-card__num">1</span>
            Scan this QR code with your authenticator app (Google Authenticator, Authy, 1Password, etc.).
          </div>
          <div class="mfa-qr">
            <img :src="mfaSetupData.qr" alt="TOTP QR code" width="180" height="180" class="mfa-qr__img" />
          </div>
          <button class="mfa-secret-toggle" @click="showMFASecret = !showMFASecret">
            <Icon name="chevron-right" :size="11" :style="showMFASecret ? 'transform:rotate(90deg)' : ''" />
            Can't scan? Enter the key manually
          </button>
          <div v-if="showMFASecret" class="mfa-setup-card__secret-row">
            <code class="mfa-setup-card__secret">{{ mfaSetupData.secret }}</code>
            <button class="btn btn--ghost" style="height: 28px; padding: 0 8px; font-size: var(--text-xs)" @click="copyToClipboard(mfaSetupData!.secret, 'Secret key')">
              <Icon name="copy" :size="11" /> Copy
            </button>
          </div>
          <div id="settings-mfa-hint" class="mfa-setup-card__step" style="margin-top: 20px">
            <span class="mfa-setup-card__num">2</span>
            Enter the 6-digit code from your app to confirm setup.
          </div>
          <label for="settings-mfa-code" class="field__label">Authenticator code</label>
          <div style="display: flex; gap: 8px; align-items: center; margin-top: 10px">
            <input
              id="settings-mfa-code"
              ref="mfaCodeInput"
              v-model="mfaCode"
              class="field__input mfa-setup-card__code-input"
              placeholder="000000"
              maxlength="6"
              inputmode="numeric"
              autocomplete="one-time-code"
              :aria-invalid="!!mfaSetupError || undefined"
              :aria-describedby="mfaSetupError ? 'settings-mfa-hint settings-mfa-error' : 'settings-mfa-hint'"
              @keydown.enter="confirmMFASetup(mfaCode)"
            />
            <button class="btn btn--primary" :disabled="mfaCode.length < 6 || confirmingMFA" @click="confirmMFASetup(mfaCode)">
              {{ confirmingMFA ? 'Verifying…' : 'Confirm' }}
            </button>
            <button class="btn btn--ghost" @click="cancelMFASetup">Cancel</button>
          </div>
          <div v-if="mfaSetupError" id="settings-mfa-error" role="alert" class="profile-error" style="margin-top: 8px">{{ mfaSetupError }}</div>
        </div>
      </template>

      <!-- MFA enabled -->
      <template v-else-if="me?.mfa_enabled">
        <div style="display: flex; align-items: center; gap: 12px">
          <span class="mfa-badge">
            <Icon name="shield-check" :size="12" />
            Enabled
          </span>
          <span style="font-size: var(--text-sm); color: var(--text-2)">Your account is protected with two-factor authentication.</span>
        </div>
        <div style="margin-top: 14px">
          <button v-if="!showReplaceMFA" class="btn btn--ghost" @click="showReplaceMFA = true; mfaSetupError = null">Replace authenticator</button>
          <form v-else @submit.prevent="startMFASetup()">
            <label class="field__label" for="mfa-current-code">Code from your current authenticator</label>
            <input id="mfa-current-code" v-model="mfaCurrentCode" class="field__input" inputmode="numeric" autocomplete="one-time-code" maxlength="6" required
              :aria-invalid="!!mfaSetupError || undefined" :aria-describedby="mfaSetupError ? 'mfa-replace-hint mfa-replace-error' : 'mfa-replace-hint'" />
            <p id="mfa-replace-hint">Your current authenticator stays active until you confirm the replacement.</p>
            <button class="btn btn--primary" type="submit" :disabled="mfaCurrentCode.length !== 6 || loadingMFASetup">Continue</button>
            <button class="btn btn--ghost" type="button" @click="showReplaceMFA = false; mfaCurrentCode = ''; mfaSetupError = null">Cancel</button>
          </form>
          <div v-if="mfaSetupError" id="mfa-replace-error" role="alert" class="profile-error">{{ mfaSetupError }}</div>
        </div>
        <div v-if="!showDisableMFA" style="margin-top: 14px">
          <button class="btn btn--ghost" style="color: var(--danger, #ef4444)" @click="showDisableMFA = true; mfaDisableError = null">
            Disable two-factor auth
          </button>
        </div>
        <div v-else style="margin-top: 14px; display: flex; flex-direction: column; gap: 10px; max-width: 280px">
          <div class="field">
            <label class="field__label" for="mfa-disable-password">Confirm your password to disable 2FA</label>
            <input id="mfa-disable-password" v-model="mfaDisablePassword" class="field__input" type="password" autocomplete="current-password"
              :aria-invalid="!!mfaDisableError || undefined" :aria-describedby="mfaDisableError ? 'mfa-disable-error' : undefined" />
          </div>
          <div v-if="mfaDisableError" id="mfa-disable-error" role="alert" class="profile-error">{{ mfaDisableError }}</div>
          <div style="display: flex; gap: 8px">
            <button class="btn btn--primary" :disabled="!mfaDisablePassword || disablingMFA" @click="disableMFA(mfaDisablePassword)" style="background: var(--danger, #ef4444); border-color: var(--danger, #ef4444)">
              {{ disablingMFA ? 'Disabling…' : 'Disable 2FA' }}
            </button>
            <button class="btn btn--ghost btn--mfa-disable-cancel" @click="showDisableMFA = false; mfaDisablePassword = ''; mfaDisableError = null">Cancel</button>
          </div>
        </div>
      </template>

    </div>
  </div>
</template>
