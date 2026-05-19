import type { ThemedToken } from 'shiki'

const PLATFORM_LANG: Record<string, string> = {
  python: 'python',
  javascript: 'javascript',
  node: 'javascript',
  typescript: 'typescript',
  go: 'go',
  ruby: 'ruby',
  java: 'java',
  php: 'php',
  csharp: 'csharp',
  dotnet: 'csharp',
  rust: 'rust',
  elixir: 'elixir',
  kotlin: 'kotlin',
  swift: 'swift',
}

export function langForPlatform(platform: string | undefined): string {
  return PLATFORM_LANG[platform ?? ''] ?? 'text'
}

let highlighterPromise: Promise<import('shiki').Highlighter> | null = null

function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = import('shiki').then(({ createHighlighter, createJavaScriptRegexEngine }) =>
      createHighlighter({
        themes: ['github-light', 'github-dark'],
        langs: [...new Set(Object.values(PLATFORM_LANG))],
        engine: createJavaScriptRegexEngine(),
      }),
    )
  }
  return highlighterPromise
}

export async function highlightBlock(code: string, lang: string): Promise<ThemedToken[][]> {
  const hl = await getHighlighter()
  const result = hl.codeToTokens(code, {
    lang,
    themes: { light: 'github-light', dark: 'github-dark' },
    defaultColor: false,
  })
  return result.tokens
}
