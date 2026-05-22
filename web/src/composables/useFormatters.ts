import { useTimezone } from './useTimezone'
import { formatTs as _formatTs, formatRel as _formatRel } from '@/utils/formatters'

export function useFormatters() {
  const tz = useTimezone()
  return {
    formatTs: (iso: string) => _formatTs(iso, tz.value),
    formatRel: (iso: string) => _formatRel(iso, tz.value),
  }
}
