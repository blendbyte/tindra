import { describe, it, expect, vi } from 'vitest'
import { langForPlatform, highlightBlock } from '../useShiki'

vi.mock('shiki/core', async importOriginal => {
 const actual = await importOriginal<typeof import('shiki/core')>()
 return { ...actual, createHighlighterCore: vi.fn(actual.createHighlighterCore) }
})

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

  it('loads only requested languages and shares initialization', async () => {
    const { createHighlighterCore } = await import('shiki/core')
    const hl = await vi.mocked(createHighlighterCore).mock.results[0].value
    expect(hl.getLoadedLanguages()).not.toContain('python')
    await Promise.all([highlightBlock('let y = 2', 'typescript'), highlightBlock('let z = 3', 'typescript')])
    expect(hl.getLoadedLanguages()).toContain('typescript')
    expect(hl.getLoadedLanguages()).not.toContain('python')
    expect(createHighlighterCore).toHaveBeenCalledTimes(1)
  })

  it('leaves unknown languages and oversized blocks intact', async () => {
    expect(await highlightBlock('<hello>\nworld', 'unknown')).toEqual([
      [{ content: '<hello>', offset: 0 }], [{ content: 'world', offset: 0 }],
    ])
    const code = 'x'.repeat(32769)
    expect((await highlightBlock(code, 'javascript'))[0][0].content).toBe(code)
  })
})

describe('supported language grammars', () => {
  it.each([
    ['python', 'print("hello")'], ['javascript', 'const value = 1'],
    ['typescript', 'const value: number = 1'], ['go', 'package main'],
    ['ruby', 'puts "hello"'], ['java', 'class App {}'],
    ['php', '<?php echo "hello";'], ['csharp', 'class App {}'],
    ['rust', 'fn main() {}'], ['elixir', 'IO.puts("hello")'],
    ['kotlin', 'fun main() {}'], ['swift', 'let value = 1'],
  ])('loads %s on demand and preserves the source text', async (lang, source) => {
    const tokens = await highlightBlock(source, lang)
    expect(tokens.map(line => line.map(token => token.content).join('')).join('\n')).toBe(source)
    expect(tokens.flat().some(token => token.htmlStyle)).toBe(true)
  })
})
