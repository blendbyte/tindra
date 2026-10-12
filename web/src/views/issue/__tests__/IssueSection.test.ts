import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import IssueSection from '../IssueSection.vue'

const KEY = 'tindra:issue:collapsed'
const stubs = { Icon: { template: '<span class="icon" />' } }

function mountSection(sectionKey = 'request', slots: Record<string, string> = {}) {
  return mount(IssueSection, {
    props: { sectionKey, title: 'HTTP Request' },
    slots: { default: '<div class="body">Body</div>', head: '<span class="extra">GET</span>', ...slots },
    global: { stubs },
  })
}

describe('IssueSection', () => {
  beforeEach(() => localStorage.removeItem(KEY))

  it('renders the title, the head slot and the body when expanded', () => {
    const wrapper = mountSection()
    expect(wrapper.classes()).toEqual(['section'])
    expect(wrapper.find('.section__head .section__title').text()).toBe('HTTP Request')
    expect(wrapper.find('.section__head .extra').text()).toBe('GET')
    expect(wrapper.find('.body').exists()).toBe(true)
    expect(wrapper.find('.section__chevron').classes()).toContain('section__chevron--open')
  })

  it('makes the head a keyboard-operable button that announces its state', () => {
    const head = mountSection().find('.section__head')
    expect(head.attributes('role')).toBe('button')
    expect(head.attributes('tabindex')).toBe('0')
    expect(head.attributes('aria-expanded')).toBe('true')
  })

  it('collapses and expands on click and stores the choice', async () => {
    const wrapper = mountSection()
    await wrapper.find('.section__head').trigger('click')
    expect(wrapper.find('.body').exists()).toBe(false)
    expect(wrapper.find('.section__head').attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('.section__chevron').classes()).not.toContain('section__chevron--open')
    expect(JSON.parse(localStorage.getItem(KEY)!).request).toBe(true)

    await wrapper.find('.section__head').trigger('click')
    expect(wrapper.find('.body').exists()).toBe(true)
    expect(JSON.parse(localStorage.getItem(KEY)!).request).toBe(false)
  })

  it('toggles with Enter and Space', async () => {
    const wrapper = mountSection()
    const head = wrapper.find('.section__head')
    await head.trigger('keydown', { key: 'Enter' })
    expect(head.attributes('aria-expanded')).toBe('false')
    await head.trigger('keydown', { key: ' ' })
    expect(head.attributes('aria-expanded')).toBe('true')
  })

  it('keeps the head visible while collapsed', async () => {
    const wrapper = mountSection()
    await wrapper.find('.section__head').trigger('click')
    expect(wrapper.find('.section__title').text()).toBe('HTTP Request')
    expect(wrapper.find('.extra').exists()).toBe(true)
  })

  it('starts collapsed for sections collapsed by default', () => {
    const wrapper = mountSection('packages')
    expect(wrapper.find('.body').exists()).toBe(false)
    expect(wrapper.find('.section__head').attributes('aria-expanded')).toBe('false')
  })

  it('restores the stored state of its own section only', () => {
    localStorage.setItem(KEY, JSON.stringify({ request: true, packages: false }))
    expect(mountSection('request').find('.body').exists()).toBe(false)
    expect(mountSection('packages').find('.body').exists()).toBe(true)
    expect(mountSection('tags').find('.body').exists()).toBe(true)
  })

  it('treats an unknown section as expanded', () => {
    expect(mountSection('something-new').find('.body').exists()).toBe(true)
  })

  it('does not toggle when a head control stops the click', async () => {
    const wrapper = mountSection('trace', { head: '<a class="link" @click.stop>View</a>' })
    await wrapper.find('.link').trigger('click')
    expect(wrapper.find('.body').exists()).toBe(true)
  })

  it('keeps choices made in another section on the page', async () => {
    const first = mountSection('request')
    const second = mountSection('tags')
    await first.find('.section__head').trigger('click')
    await second.find('.section__head').trigger('click')
    const stored = JSON.parse(localStorage.getItem(KEY)!)
    expect(stored.request).toBe(true)
    expect(stored.tags).toBe(true)
    expect(stored.packages).toBe(true)
  })
})
