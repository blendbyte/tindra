import { describe, it, expect, vi } from 'vitest'
import { langForPlatform, highlightBlock } from '../useShiki'

vi.mock('shiki', () => ({
  createHighlighter: vi.fn().mockResolvedValue({
    codeToTokens: vi.fn().mockReturnValue({
      tokens: [[{ content: 'const', color: { light: '#000066', dark: '#0000cc' } }]],
    }),
  }),
  createJavaScriptRegexEngine: vi.fn().mockReturnValue({}),
}))

describe('langForPlatform', () => {
  it.each([
    ['python', 'python'],
    ['javascript', 'javascript'],
    ['node', 'javascript'],
    ['typescript', 'typescript'],
    ['go', 'go'],
    ['ruby', 'ruby'],
    ['java', 'java'],
    ['php', 'php'],
    ['csharp', 'csharp'],
    ['dotnet', 'csharp'],
    ['rust', 'rust'],
    ['elixir', 'elixir'],
    ['kotlin', 'kotlin'],
    ['swift', 'swift'],
  ])('maps %s → %s', (platform, expected) => {
    expect(langForPlatform(platform)).toBe(expected)
  })

  it('falls back to text for unknown platform', () => {
    expect(langForPlatform('cobol')).toBe('text')
  })

  it('falls back to text for undefined', () => {
    expect(langForPlatform(undefined)).toBe('text')
  })

  it('falls back to text for empty string', () => {
    expect(langForPlatform('')).toBe('text')
  })
})

describe('highlightBlock', () => {
  it('returns a 2D token array', async () => {
    const tokens = await highlightBlock('const x = 1', 'javascript')
    expect(Array.isArray(tokens)).toBe(true)
    expect(Array.isArray(tokens[0])).toBe(true)
  })

  it('returns token objects with content', async () => {
    const tokens = await highlightBlock('const x = 1', 'javascript')
    expect(tokens[0][0]).toHaveProperty('content')
  })

  it('calls the highlighter with correct parameters', async () => {
    const shiki = await import('shiki')
    const mockHighlighter = await (shiki.createHighlighter as ReturnType<typeof vi.fn>).mock.results[0].value

    await highlightBlock('let y = 2', 'typescript')

    expect(mockHighlighter.codeToTokens).toHaveBeenCalledWith('let y = 2', expect.objectContaining({
      lang: 'typescript',
      themes: expect.objectContaining({ light: 'github-light', dark: 'github-dark' }),
    }))
  })

  it('reuses the cached highlighter instance across calls', async () => {
    const shiki = await import('shiki')
    const createHighlighterSpy = shiki.createHighlighter as ReturnType<typeof vi.fn>
    const callsBefore = createHighlighterSpy.mock.calls.length

    await highlightBlock('a', 'go')
    await highlightBlock('b', 'go')

    // createHighlighter should not have been called again (singleton reuse)
    expect(createHighlighterSpy.mock.calls.length).toBe(callsBefore)
  })
})
