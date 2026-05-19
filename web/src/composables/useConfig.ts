import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'

interface AppConfig {
  public_url: string
}

export function useConfig() {
  const { data } = useQuery({
    queryKey: ['config'],
    queryFn: () => apiFetch<AppConfig>('/api/config'),
    staleTime: Infinity,
  })

  const baseUrl = computed(() => {
    const url = data.value?.public_url ?? ''
    return url.replace(/\/$/, '')
  })

  // Sentry DSN format: https://{public_key}@{host}/{project_id}
  // The SDK derives the envelope URL as {host}/api/{project_id}/envelope/
  // which matches Tindra's ingest endpoint.
  function dsnFor(publicKey: string, projectId: string): string {
    const base = baseUrl.value || window.location.origin
    try {
      const url = new URL(base)
      return `${url.protocol}//${publicKey}@${url.host}/${projectId}`
    } catch {
      return `${base}/${projectId}`
    }
  }

  return { baseUrl, dsnFor }
}
