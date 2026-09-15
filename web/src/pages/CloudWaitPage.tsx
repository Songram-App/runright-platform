import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { fetchCloudTenantStatus } from '../api'
import LogoMark from '../components/LogoMark'

// Polls /api/v1/cloud/status until the tenant's dedicated instance is ready,
// then hands off to its one-time claim link (which logs the browser into
// that instance and lands on its own dashboard).
export default function CloudWaitPage() {
  const [params] = useSearchParams()
  const tenantId = params.get('tenant') ?? ''
  const [error, setError] = useState<string | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!tenantId) {
      setError('Missing workspace reference.')
      return
    }
    let cancelled = false

    async function poll() {
      try {
        const data = await fetchCloudTenantStatus(tenantId)
        if (cancelled) return
        if (data.status === 'active' && data.claim_url) {
          window.location.href = data.claim_url
          return
        }
        if (data.status === 'failed') {
          setError(data.error || 'Please contact support.')
          return
        }
      } catch {
        // transient network hiccup — keep polling
      }
      if (!cancelled) timerRef.current = setTimeout(poll, 2500)
    }
    poll()

    return () => {
      cancelled = true
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [tenantId])

  return (
    <div className="flex items-center justify-center min-h-screen bg-[#1A0F02] text-[#E8C458] px-4 font-sans">
      <div className="max-w-sm text-center">
        <div className="flex flex-col items-center mb-6">
          <LogoMark size={32} color="currentColor" />
        </div>
        {!error && (
          <div className="w-9 h-9 mx-auto mb-5 rounded-full border-[3px] border-[#3a2510] border-t-[#B8860B] animate-spin" />
        )}
        <h2 className="text-lg font-bold mb-2">
          {error ? 'Setup failed.' : 'Setting up your workspace\u2026'}
        </h2>
        {!error && <p className="text-sm text-[#C4A882]">This usually takes under a minute.</p>}
        {error && <p className="text-sm text-red">{error}</p>}
      </div>
    </div>
  )
}
