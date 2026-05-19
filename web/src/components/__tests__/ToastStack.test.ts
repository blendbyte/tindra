import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ToastStack from '../ToastStack.vue'
import { useToast } from '@/composables/useToast'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runAllTimers()
  vi.useRealTimers()
  useToast().toasts.value = []
})

describe('ToastStack', () => {
  it('renders an aria-live region', () => {
    const wrapper = mount(ToastStack)
    expect(wrapper.find('[aria-live="polite"]').exists()).toBe(true)
  })

  it('renders nothing when there are no toasts', () => {
    const wrapper = mount(ToastStack)
    expect(wrapper.findAll('.toast')).toHaveLength(0)
  })

  it('renders a toast with the correct message', async () => {
    useToast().show('Something went wrong', 'error')
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.find('.toast__msg').text()).toBe('Something went wrong')
  })

  it('renders one element per toast', async () => {
    const { show } = useToast()
    show('First', 'info')
    show('Second', 'success')
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.findAll('.toast')).toHaveLength(2)
  })

  it('applies toast--success class for success type', async () => {
    useToast().show('Saved', 'success')
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.find('.toast').classes()).toContain('toast--success')
  })

  it('applies toast--error class for error type', async () => {
    useToast().show('Failed', 'error')
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.find('.toast').classes()).toContain('toast--error')
  })

  it('applies toast--info class for info type', async () => {
    useToast().show('Heads up', 'info')
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.find('.toast').classes()).toContain('toast--info')
  })

  it('sets the leaving class when dismiss is called', async () => {
    const { show, toasts } = useToast()
    show('Hello', 'info')
    const wrapper = mount(ToastStack)
    await nextTick()

    await wrapper.find('[aria-label="Dismiss"]').trigger('click')

    expect(toasts.value[0].leaving).toBe(true)
  })

  it('removes the toast after 180ms following dismiss', async () => {
    const { show, toasts } = useToast()
    show('Hello', 'info')
    const wrapper = mount(ToastStack)
    await nextTick()

    await wrapper.find('[aria-label="Dismiss"]').trigger('click')
    vi.advanceTimersByTime(180)
    await nextTick()

    expect(toasts.value).toHaveLength(0)
  })

  it('does not show an undo button without an onUndo callback', async () => {
    useToast().show('Hello', 'info')
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.find('.toast__undo').exists()).toBe(false)
  })

  it('shows an undo button when onUndo is provided', async () => {
    useToast().show('Project created', vi.fn())
    const wrapper = mount(ToastStack)
    await nextTick()
    expect(wrapper.find('.toast__undo').exists()).toBe(true)
  })

  it('calls onUndo and sets leaving when the undo button is clicked', async () => {
    const onUndo = vi.fn()
    const { show, toasts } = useToast()
    show('Project created', onUndo)
    const wrapper = mount(ToastStack)
    await nextTick()

    await wrapper.find('.toast__undo').trigger('click')

    expect(onUndo).toHaveBeenCalledOnce()
    expect(toasts.value[0].leaving).toBe(true)
  })
})
