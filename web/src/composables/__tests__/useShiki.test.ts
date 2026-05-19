import { describe, it, expect } from 'vitest'
import { langForPlatform } from '../useShiki'

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
