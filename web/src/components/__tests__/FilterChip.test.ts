import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import FilterChip from '../FilterChip.vue'

const defaultProps = {
  label: 'Status',
  value: 'All',
  options: ['All', 'Open', 'Resolved'],
}

describe('FilterChip', () => {
  it('renders the label and current value', () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    expect(wrapper.find('.filterchip__label').text()).toBe('Status:')
    expect(wrapper.find('.filterchip__value').text()).toBe('All')
  })

  it('does not show the dropdown initially', () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    expect(wrapper.find('.popover').exists()).toBe(false)
  })

  it('opens the dropdown on button click', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    await wrapper.find('.filterchip').trigger('click')
    expect(wrapper.find('.popover').exists()).toBe(true)
  })

  it('toggles closed on a second click', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    await wrapper.find('.filterchip').trigger('click')
    await wrapper.find('.filterchip').trigger('click')
    expect(wrapper.find('.popover').exists()).toBe(false)
  })

  it('lists all options when open', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    await wrapper.find('.filterchip').trigger('click')
    const items = wrapper.findAll('.popover__item')
    expect(items).toHaveLength(3)
    expect(items[0].text()).toBe('All')
    expect(items[1].text()).toBe('Open')
    expect(items[2].text()).toBe('Resolved')
  })

  it('emits change with the selected value', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    await wrapper.find('.filterchip').trigger('click')
    await wrapper.findAll('.popover__item')[1].trigger('click')
    expect(wrapper.emitted('change')).toEqual([['Open']])
  })

  it('closes after selecting an option', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    await wrapper.find('.filterchip').trigger('click')
    await wrapper.findAll('.popover__item')[1].trigger('click')
    expect(wrapper.find('.popover').exists()).toBe(false)
  })

  it('marks the currently active option', async () => {
    const wrapper = mount(FilterChip, { props: { ...defaultProps, value: 'Open' } })
    await wrapper.find('.filterchip').trigger('click')
    const items = wrapper.findAll('.popover__item')
    expect(items[1].classes()).toContain('popover__item--active')
    expect(items[0].classes()).not.toContain('popover__item--active')
    expect(items[2].classes()).not.toContain('popover__item--active')
  })

  it('applies filterchip--active when value is not the first option', () => {
    const wrapper = mount(FilterChip, { props: { ...defaultProps, value: 'Open' } })
    expect(wrapper.find('.filterchip').classes()).toContain('filterchip--active')
  })

  it('does not apply filterchip--active when value is the first option', () => {
    const wrapper = mount(FilterChip, { props: defaultProps })
    expect(wrapper.find('.filterchip').classes()).not.toContain('filterchip--active')
  })

  it('closes on mousedown outside the component', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps, attachTo: document.body })
    await wrapper.find('.filterchip').trigger('click')
    expect(wrapper.find('.popover').exists()).toBe(true)

    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()

    expect(wrapper.find('.popover').exists()).toBe(false)
    wrapper.unmount()
  })

  it('stays open on mousedown inside the component', async () => {
    const wrapper = mount(FilterChip, { props: defaultProps, attachTo: document.body })
    await wrapper.find('.filterchip').trigger('click')

    wrapper.element.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()

    expect(wrapper.find('.popover').exists()).toBe(true)
    wrapper.unmount()
  })
})
