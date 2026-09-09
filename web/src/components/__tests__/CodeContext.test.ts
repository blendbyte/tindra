import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('@/composables/useShiki', () => ({
  highlightBlock: vi.fn().mockResolvedValue([]),
  langForPlatform: vi.fn((platform: string | undefined) => {
    const map: Record<string, string> = {
      python: 'python',
      javascript: 'javascript',
    }
    return map[platform ?? ''] ?? 'text'
  }),
}))

import CodeContext from '../CodeContext.vue'

const defaultProps = {
  preContext: ['def foo():', '  x = 1'],
  contextLine: '  return x',
  postContext: ['', 'foo()'],
  lineno: 10,
}

describe('CodeContext', () => {
  it('mounts without errors', () => {
    const wrapper = mount(CodeContext, { props: defaultProps })
    expect(wrapper.find('.stack__source').exists()).toBe(true)
  })

  it('renders all lines (pre + context + post) in the loading state', () => {
    const wrapper = mount(CodeContext, { props: defaultProps })
    const lines = wrapper.findAll('.stack__source-line')
    const total = defaultProps.preContext.length + 1 + defaultProps.postContext.length
    expect(lines).toHaveLength(total)
  })

  it('highlights the context line with the --hi modifier', () => {
    const wrapper = mount(CodeContext, { props: defaultProps })
    const lines = wrapper.findAll('.stack__source-line')
    const hiLines = lines.filter(l => l.classes().includes('stack__source-line--hi'))
    expect(hiLines).toHaveLength(1)
  })

  it('the highlighted line is at the correct index (preContext.length)', () => {
    const wrapper = mount(CodeContext, { props: defaultProps })
    const lines = wrapper.findAll('.stack__source-line')
    expect(lines[defaultProps.preContext.length].classes()).toContain('stack__source-line--hi')
  })

  it('displays correct line numbers', () => {
    const wrapper = mount(CodeContext, { props: defaultProps })
    const lineNums = wrapper.findAll('.stack__source-ln')
    const firstLineNo = defaultProps.lineno - defaultProps.preContext.length
    expect(lineNums[0].text()).toBe(String(firstLineNo))
    expect(lineNums[defaultProps.preContext.length].text()).toBe(String(defaultProps.lineno))
  })

  it('renders with empty pre and post context', () => {
    const wrapper = mount(CodeContext, {
      props: {
        preContext: [],
        contextLine: 'raise Exception()',
        postContext: [],
        lineno: 5,
      },
    })
    const lines = wrapper.findAll('.stack__source-line')
    expect(lines).toHaveLength(1)
    expect(lines[0].classes()).toContain('stack__source-line--hi')
    expect(lines[0].find('.stack__source-ln').text()).toBe('5')
  })

  it('renders the source code text in the loading state', () => {
    const wrapper = mount(CodeContext, {
      props: {
        preContext: [],
        contextLine: 'const x = 42',
        postContext: [],
        lineno: 1,
      },
    })
    expect(wrapper.find('.stack__source-code').text()).toContain('const x = 42')
  })

  it('renders with a platform prop without error', () => {
    const wrapper = mount(CodeContext, {
      props: { ...defaultProps, platform: 'python' },
    })
    expect(wrapper.find('.stack__source').exists()).toBe(true)
  })
})

describe('highlighting failures', () => {
  it('keeps readable source and line numbers after a chunk fails, then recovers on new source', async () => {
    const { highlightBlock } = await import('@/composables/useShiki')
    const { flushPromises } = await import('@vue/test-utils')
    vi.mocked(highlightBlock).mockRejectedValueOnce(new Error('Failed to fetch grammar chunk'))
    const wrapper = mount(CodeContext, { props: { ...defaultProps, platform: 'python' } })
    await flushPromises()
    expect(wrapper.findAll('.stack__source-code').map(line => line.text())).toEqual([
      'def foo():', 'x = 1', 'return x', '', 'foo()',
    ])
    expect(wrapper.get('.stack__source-line--hi .stack__source-ln').text()).toBe('10')
    expect(wrapper.get('.stack__source-line--hi .stack__source-code').findAll('span')).toHaveLength(0)

    vi.mocked(highlightBlock).mockResolvedValueOnce([[{ content: 'return 42', offset: 0 }]])
    await wrapper.setProps({ preContext: [], contextLine: 'return 42', postContext: [] })
    await flushPromises()
    expect(wrapper.get('.stack__source-code span').text()).toBe('return 42')
    expect(wrapper.get('.stack__source-ln').text()).toBe('10')
    wrapper.unmount()
  })
})
