import { useState, useEffect } from 'react'
import { fetchUsage, type UsageSummary } from '../api'
import { RequestQuoteModal } from './RequestQuoteModal'

// Shows nothing on unmetered (self-hosted) deployments or when usage is
// comfortably under the Free plan's caps. Once a team is near or at a cap,
// nudges them toward "Request a Quote" instead of silently blocking them.
export function UsageCapBanner() {
  const [usage, setUsage] = useState<UsageSummary | null>(null)
  const [quoteOpen, setQuoteOpen] = useState(false)
  const [dismissed, setDismissed] = useState(false)

  useEffect(() => {
    fetchUsage()
      .then(setUsage)
      .catch(() => { /* fail silent — never block the dashboard on this */ })
  }, [])

  if (!usage || !usage.metered || dismissed) return null

  const jobsPct = usage.max_jobs_per_month ? (usage.jobs_this_month ?? 0) / usage.max_jobs_per_month : 0
  const reposPct = usage.max_repos ? (usage.repos_connected ?? 0) / usage.max_repos : 0
  const atCap = usage.jobs_at_cap || usage.repos_at_cap
  const nearCap = !atCap && (jobsPct >= 0.8 || reposPct >= 0.8)

  if (!atCap && !nearCap) return null

  const reason = usage.jobs_at_cap ? 'jobs_per_month' : usage.repos_at_cap ? 'repos' : undefined

  return (
    <>
      <div
        className={`flex flex-wrap items-center gap-3 px-4 py-3 border-b text-sm ${
          atCap
            ? 'bg-[var(--red)]/10 border-[var(--red)]/30 text-[var(--red)]'
            : 'bg-[var(--gold)]/10 border-[var(--gold)]/30 text-[var(--text)]'
        }`}
      >
        <span className="font-medium">
          {atCap
            ? `You've reached the ${usage.plan_name ?? 'Free'} plan limit for ${usage.jobs_at_cap ? 'jobs analyzed this month' : 'connected repositories'}.`
            : `You're approaching the ${usage.plan_name ?? 'Free'} plan limit (${usage.jobs_this_month ?? 0}/${usage.max_jobs_per_month} jobs this month, ${usage.repos_connected ?? 0}/${usage.max_repos} repos).`}
        </span>
        <button
          onClick={() => setQuoteOpen(true)}
          className="px-3 py-1 rounded-lg text-xs font-semibold bg-[var(--gold)] text-[var(--cream)] hover:opacity-90 transition-colors"
        >
          Request a Quote
        </button>
        <button
          onClick={() => setDismissed(true)}
          className="ml-auto text-xs opacity-60 hover:opacity-100"
          aria-label="Dismiss"
        >
          Dismiss
        </button>
      </div>
      <RequestQuoteModal open={quoteOpen} reason={reason} onClose={() => setQuoteOpen(false)} />
    </>
  )
}
