import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { computed, ref } from 'vue'

const { showToast, push } = vi.hoisted(() => ({ showToast: vi.fn(), push: {} as Record<string, any> }))

vi.mock('@/composables/useToast', () => ({ useToast: () => ({ show: showToast }) }))
vi.mock('@/composables/useFormatters', () => ({ useFormatters: () => ({ formatRel: () => '2m ago' }) }))
vi.mock('@/composables/usePushNotifications', () => ({ usePushNotifications: () => push }))

import PushDevices from '../PushDevices.vue'

const device = (id: string, endpoint: string, extra: Record<string, unknown> = {}) => ({
  id, endpoint, label: `Device ${id}`, failure_count: 0, last_success_at: null, created_at: '2026-11-14T00:00:00Z', ...extra,
})

function setup({ blocker = null as string | null, devices = [] as ReturnType<typeof device>[], current = null as string | null } = {}) {
  const devicesRef = ref(devices)
  const currentRef = ref(current)
  Object.assign(push, {
    devices: devicesRef,
    currentEndpoint: currentRef,
    permission: ref('default'),
    busy: ref(false),
    blocker: ref(blocker),
    enabledHere: computed(() => !!currentRef.value && devicesRef.value.some(d => d.endpoint === currentRef.value)),
    refresh: vi.fn(() => Promise.resolve()),
    enable: vi.fn(async () => {
      devicesRef.value = [device('new', 'https://push/new')]
      currentRef.value = 'https://push/new'
      return devicesRef.value[0]
    }),
    disable: vi.fn(async () => { currentRef.value = null }),
    remove: vi.fn(async (id: string) => { devicesRef.value = devicesRef.value.filter(d => d.id !== id) }),
    sendTest: vi.fn(() => Promise.resolve()),
  })
}

async function mountIt() {
  const w = mount(PushDevices)
  await flushPromises()
  return w
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('PushDevices', () => {
  it('turns push on for this device', async () => {
    setup()
    const w = await mountIt()
    expect(push.refresh).toHaveBeenCalled()
    const toggle = w.find('button[role="switch"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await flushPromises()
    expect(push.enable).toHaveBeenCalled()
    expect(showToast).toHaveBeenCalledWith('Push notifications turned on for this device')
    expect(w.find('button[role="switch"]').attributes('aria-checked')).toBe('true')
    expect(w.text()).toContain('This device')
  })

  it('turns push off for this device', async () => {
    setup({ devices: [device('d1', 'https://push/here')], current: 'https://push/here' })
    const w = await mountIt()
    await w.find('button[role="switch"]').trigger('click')
    await flushPromises()
    expect(push.disable).toHaveBeenCalled()
    expect(showToast).toHaveBeenCalledWith('Push notifications turned off on this device')
  })

  it('reports a denied permission', async () => {
    setup()
    push.enable = vi.fn(async () => { push.permission.value = 'denied'; return null })
    const w = await mountIt()
    await w.find('button[role="switch"]').trigger('click')
    await flushPromises()
    expect(showToast).toHaveBeenCalledWith('Notifications are blocked for this site', 'error')
  })

  it('shows errors from enabling', async () => {
    setup()
    push.enable = vi.fn(() => Promise.reject(new Error('push service unreachable')))
    const w = await mountIt()
    await w.find('button[role="switch"]').trigger('click')
    await flushPromises()
    expect(showToast).toHaveBeenCalledWith('push service unreachable', 'error')
  })

  it.each([
    ['insecure', 'HTTPS'],
    ['ios-install', 'Add to Home Screen'],
    ['unsupported', 'does not support'],
    ['disabled', 'not available on this server'],
    ['denied', 'blocked for this site'],
  ])('explains the %s blocker and disables the switch', async (blocker, text) => {
    setup({ blocker })
    const w = await mountIt()
    expect(w.find('[data-testid="push-blocker"]').text()).toContain(text)
    expect(w.find('button[role="switch"]').attributes('disabled')).toBeDefined()
  })

  it('lists devices and sends tests or removes them', async () => {
    setup({ devices: [device('d1', 'https://push/a', { last_success_at: '2026-11-14T00:00:00Z', failure_count: 2 }), device('d2', 'https://push/b')] })
    const w = await mountIt()
    const items = w.findAll('.push-devices__item')
    expect(items).toHaveLength(2)
    expect(items[0]!.text()).toContain('Device d1')
    expect(items[0]!.text()).toContain('last delivered 2m ago')
    expect(items[0]!.text()).toContain('2 failed deliveries')

    await items[0]!.findAll('button').find(b => b.text() === 'Send test')!.trigger('click')
    await flushPromises()
    expect(push.sendTest).toHaveBeenCalledWith('d1')
    expect(showToast).toHaveBeenCalledWith('Test notification sent')

    push.sendTest = vi.fn(() => Promise.reject(new Error('device "Device d2" unsubscribed and was removed')))
    await w.findAll('.push-devices__item')[1]!.findAll('button').find(b => b.text() === 'Send test')!.trigger('click')
    await flushPromises()
    expect(showToast).toHaveBeenCalledWith('device "Device d2" unsubscribed and was removed', 'error')
    expect(push.refresh).toHaveBeenCalledTimes(2)

    await w.find('button[aria-label="Remove Device d1"]').trigger('click')
    await flushPromises()
    expect(push.remove).toHaveBeenCalledWith('d1')
    expect(showToast).toHaveBeenCalledWith('Device removed')
    expect(w.findAll('.push-devices__item')).toHaveLength(1)
  })

  it('survives a failing initial load', async () => {
    setup()
    push.refresh = vi.fn(() => Promise.reject(new Error('503')))
    const w = await mountIt()
    expect(w.find('button[role="switch"]').attributes('disabled')).toBeUndefined()
  })
})
