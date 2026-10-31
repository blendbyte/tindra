// "1 issue", "3 issues".
export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}
