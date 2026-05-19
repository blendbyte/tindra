import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import IgnoreButton from '../IgnoreButton.vue'

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2025-06-01T00:00:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
})

describe('IgnoreButton', () => {
  describe('main Ignore button', () => {
    it('emits ignore with just status when clicked', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__main').trigger('click')
      expect(wrapper.emitted('ignore')).toEqual([[{ status: 'ignored' }]])
    })

    it('is disabled when disabled prop is true', () => {
      const wrapper = mount(IgnoreButton, { props: { disabled: true } })
      expect(wrapper.find<HTMLButtonElement>('.ignore-btn__main').element.disabled).toBe(true)
    })
  })

  describe('chevron / popover', () => {
    it('opens the popover on chevron click', async () => {
      const wrapper = mount(IgnoreButton)
      expect(wrapper.find('.popover').exists()).toBe(false)
      await wrapper.find('.ignore-btn__chevron').trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(true)
    })

    it('chevron is disabled when disabled prop is true', () => {
      const wrapper = mount(IgnoreButton, { props: { disabled: true } })
      expect(wrapper.find<HTMLButtonElement>('.ignore-btn__chevron').element.disabled).toBe(true)
    })

    it('closes on mousedown outside', async () => {
      const wrapper = mount(IgnoreButton, { attachTo: document.body })
      await wrapper.find('.ignore-btn__chevron').trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(true)

      document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
      await nextTick()

      expect(wrapper.find('.popover').exists()).toBe(false)
      wrapper.unmount()
    })
  })

  describe('time limit options', () => {
    it('emits ignore with ignore_until set to 30 minutes from now', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const items = wrapper.findAll('.popover__item')
      const thirtyMin = items.find((i) => i.text() === 'For 30 minutes')!
      await thirtyMin.trigger('click')

      const emitted = wrapper.emitted('ignore') as [unknown[]][]
      const payload = emitted[0][0] as { status: string; ignore_until: string }
      expect(payload.status).toBe('ignored')
      const until = new Date(payload.ignore_until)
      expect(until.getTime()).toBe(new Date('2025-06-01T00:00:00Z').getTime() + 30 * 60 * 1000)
    })

    it('closes the popover after selecting a time option', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')
      await wrapper.findAll('.popover__item')[0].trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(false)
    })
  })

  describe('custom date', () => {
    it('shows the date input when Custom date option is clicked', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const customItem = wrapper.findAll('.popover__item').find((i) => i.text().includes('Custom date'))!
      await customItem.trigger('click')

      expect(wrapper.find('.ignore-btn__custom').exists()).toBe(true)
    })

    it('emits ignore with the custom date on OK click', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const customItem = wrapper.findAll('.popover__item').find((i) => i.text().includes('Custom date'))!
      await customItem.trigger('click')

      await wrapper.find('.ignore-btn__date-input').setValue('2025-12-31T23:59')
      await wrapper.find('.ignore-btn__date-ok').trigger('click')

      const emitted = wrapper.emitted('ignore') as [unknown[]][]
      const payload = emitted[0][0] as { status: string; ignore_until: string }
      expect(payload.status).toBe('ignored')
      expect(payload.ignore_until).toContain('2025-12-31')
    })

    it('does not emit when date input is empty and OK is clicked', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const customItem = wrapper.findAll('.popover__item').find((i) => i.text().includes('Custom date'))!
      await customItem.trigger('click')
      await wrapper.find('.ignore-btn__date-ok').trigger('click')

      expect(wrapper.emitted('ignore')).toBeUndefined()
    })
  })

  describe('count limit options', () => {
    it('emits ignore with ignore_count_limit for "1 time"', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const oneTime = wrapper.findAll('.popover__item').find((i) => i.text() === '1 time')!
      await oneTime.trigger('click')

      expect(wrapper.emitted('ignore')).toEqual([[{ status: 'ignored', ignore_count_limit: 1 }]])
    })

    it('emits ignore with ignore_count_limit for "10 times"', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const tenTimes = wrapper.findAll('.popover__item').find((i) => i.text() === '10 times')!
      await tenTimes.trigger('click')

      expect(wrapper.emitted('ignore')).toEqual([[{ status: 'ignored', ignore_count_limit: 10 }]])
    })

    it('closes the popover after selecting a count option', async () => {
      const wrapper = mount(IgnoreButton)
      await wrapper.find('.ignore-btn__chevron').trigger('click')

      const oneTime = wrapper.findAll('.popover__item').find((i) => i.text() === '1 time')!
      await oneTime.trigger('click')

      expect(wrapper.find('.popover').exists()).toBe(false)
    })
  })
})
