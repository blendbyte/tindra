import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ create: vi.fn(), load: vi.fn(), tokens: vi.fn(), loaded: vi.fn() }))
vi.mock('shiki/core', () => ({ createHighlighterCore: mocks.create }))
vi.mock('shiki/engine/javascript', () => ({ createJavaScriptRegexEngine: () => ({}) }))

beforeEach(() => {
  vi.resetModules()
  vi.resetAllMocks()
  mocks.loaded.mockReturnValue([])
  mocks.load.mockResolvedValue(undefined)
  mocks.tokens.mockReturnValue({ tokens: [[{ content: 'highlighted' }]] })
  mocks.create.mockResolvedValue({ getLoadedLanguages: mocks.loaded, loadLanguage: mocks.load, codeToTokens: mocks.tokens })
})

describe('highlighter recovery', () => {
  it('retries initialization after a failed load', async () => {
    mocks.create.mockRejectedValueOnce(new Error('engine unavailable'))
    const { highlightBlock } = await import('../useShiki')
    await expect(highlightBlock('const x = 1', 'javascript')).rejects.toThrow('engine unavailable')
    await expect(highlightBlock('const x = 1', 'javascript')).resolves.toEqual([[{ content: 'highlighted' }]])
    expect(mocks.create).toHaveBeenCalledTimes(2)
  })

  it('shares a failed language load and lets later requests retry it', async () => {
    let rejectLoad!: (error: Error) => void
    mocks.load.mockImplementationOnce(() => new Promise<void>((_, reject) => { rejectLoad = reject }))
    const { highlightBlock } = await import('../useShiki')
    const first = highlightBlock('const x = 1', 'javascript')
    const second = highlightBlock('const y = 2', 'javascript')
    const settled = Promise.allSettled([first, second])
    await vi.waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(1))
    rejectLoad(new Error('grammar unavailable'))
    expect((await settled).map(result => result.status)).toEqual(['rejected', 'rejected'])
    expect(mocks.tokens).not.toHaveBeenCalled()
    await expect(highlightBlock('const z = 3', 'javascript')).resolves.toEqual([[{ content: 'highlighted' }]])
    expect(mocks.load).toHaveBeenCalledTimes(2)
    expect(mocks.create).toHaveBeenCalledTimes(1)
  })

  it('does not initialize the engine for plain text or oversized source', async () => {
    const { highlightBlock } = await import('../useShiki')
    expect(await highlightBlock('<b>text</b>\n', 'text')).toEqual([
      [{ content: '<b>text</b>', offset: 0 }], [{ content: '', offset: 0 }],
    ])
    const source = 'x'.repeat(32769)
    expect(await highlightBlock(source, 'python')).toEqual([[{ content: source, offset: 0 }]])
    expect(mocks.create).not.toHaveBeenCalled()
  })
})

describe('independent language loading', () => {
  it('loads Java when no other grammar has registered it as a dependency', async () => {
    const { highlightBlock } = await import('../useShiki')
    await highlightBlock('class App {}', 'java')
    expect(mocks.load).toHaveBeenCalledTimes(1)
    expect(mocks.load.mock.calls[0][0]).toEqual(expect.arrayContaining([
      expect.objectContaining({ name: 'java' }),
    ]))
    expect(mocks.tokens).toHaveBeenCalledWith('class App {}', expect.objectContaining({ lang: 'java' }))
  })
})
