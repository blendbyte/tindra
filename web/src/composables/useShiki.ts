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

const languages = {
  python: () => import('shiki/langs/python.mjs'),
  javascript: () => import('shiki/langs/javascript.mjs'),
  typescript: () => import('shiki/langs/typescript.mjs'),
  go: () => import('shiki/langs/go.mjs'),
  ruby: () => import('shiki/langs/ruby.mjs'),
  java: () => import('shiki/langs/java.mjs'),
  php: () => import('shiki/langs/php.mjs'),
  csharp: () => import('shiki/langs/csharp.mjs'),
  rust: () => import('shiki/langs/rust.mjs'),
  elixir: () => import('shiki/langs/elixir.mjs'),
  kotlin: () => import('shiki/langs/kotlin.mjs'),
  swift: () => import('shiki/langs/swift.mjs'),
}

let highlighterPromise: Promise<import('shiki/core').HighlighterCore> | null = null
const pendingLanguages = new Map<string, Promise<void>>()

function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = Promise.all([
      import('shiki/core'), import('shiki/engine/javascript'),
      import('shiki/themes/github-light.mjs'), import('shiki/themes/github-dark.mjs'),
    ]).then(([{ createHighlighterCore }, { createJavaScriptRegexEngine }, light, dark]) =>
      createHighlighterCore({ themes: [light.default, dark.default], langs: [], engine: createJavaScriptRegexEngine() }),
    ).catch(error => { highlighterPromise = null; throw error })
  }
  return highlighterPromise
}

export async function highlightBlock(code: string, lang: string): Promise<ThemedToken[][]> {
  // Plain text and oversized source lines remain readable without loading grammars.
  const loader = languages[lang as keyof typeof languages]
  if (!loader || code.length > 32_768) return code.split('\n').map(content => [{ content, offset: 0 }])
  const hl = await getHighlighter()
  if (!hl.getLoadedLanguages().includes(lang)) {
    let pending = pendingLanguages.get(lang)
    if (!pending) {
      pending = loader().then(module => hl.loadLanguage(module.default)).finally(() => pendingLanguages.delete(lang))
      pendingLanguages.set(lang, pending)
    }
    await pending
  }
  return hl.codeToTokens(code, {
    lang, themes: { light: 'github-light', dark: 'github-dark' }, defaultColor: false,
  }).tokens
}
