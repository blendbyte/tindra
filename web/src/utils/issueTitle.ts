// "TypeError: Cannot read…" splits into the exception type and the message.
// Titles without a type prefix, or with a colon inside a long sentence, stay
// whole. Every list that shows issue titles uses this, so a title reads the
// same on the dashboard, the issue list and a release.
export function splitTitle(title: string): { type: string; message: string } {
  const i = title.indexOf(':')
  if (i <= 0 || i > 80 || /\s/.test(title.slice(0, i))) return { type: '', message: title }
  return { type: title.slice(0, i), message: title.slice(i + 1).trim() }
}
