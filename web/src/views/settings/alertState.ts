import { plural } from '@/utils/plural'

// What the alert rules will do, in one sentence. `silent` is true when no rule
// can notify anyone, which pages flag in the warning colour: a paused setup
// looks exactly like a quiet one otherwise.
export function alertVerdict(rules: { enabled: boolean }[]): { text: string; silent: boolean } {
  const on = rules.filter(r => r.enabled).length
  const paused = rules.length - on
  if (!rules.length) return { text: 'No alert rules yet. Nothing will notify you.', silent: true }
  if (!on) {
    const all = rules.length === 1 ? 'The only rule is paused.' : `All ${rules.length} rules are paused.`
    return { text: `${all} Nothing will notify you.`, silent: true }
  }
  if (!paused) return { text: rules.length === 1 ? 'The rule is on.' : `All ${rules.length} rules are on.`, silent: false }
  return { text: `${on} of ${plural(rules.length, 'rule', 'rules')} on, ${paused} paused.`, silent: false }
}
