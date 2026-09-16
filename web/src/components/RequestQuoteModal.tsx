import { useState, useEffect, useRef } from 'react'
import { submitQuoteRequest, type QuoteRequestInput } from '../api'

interface RequestQuoteModalProps {
  open: boolean
  reason?: QuoteRequestInput['reason']
  onClose: () => void
}

const REASON_COPY: Record<string, string> = {
  jobs_per_month: "you've hit the Free plan's monthly job limit",
  repos: "you've hit the Free plan's connected-repository limit",
  members: "you've hit the Free plan's team member limit",
  proactive: "you're interested in a paid plan",
}

export function RequestQuoteModal({ open, reason, onClose }: RequestQuoteModalProps) {
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [company, setCompany] = useState('')
  const [message, setMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  const firstFieldRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (open) {
      setDone(false)
      setError('')
      firstFieldRef.current?.focus()
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const handle = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handle)
    return () => document.removeEventListener('keydown', handle)
  }, [open, onClose])

  if (!open) return null

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      await submitQuoteRequest({ name, email, company, message, reason })
      setDone(true)
    } catch {
      setError('Unable to submit your request — please try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" role="dialog" aria-modal="true">
      <div className="absolute inset-0 bg-[var(--text)]/40 backdrop-blur-sm" onClick={onClose} />
      <div className="relative bg-[var(--cream)] border border-[var(--border)] rounded-xl shadow-[var(--shadow-lg)] max-w-md w-full p-6">
        {done ? (
          <>
            <h3 className="font-serif text-lg font-bold text-[var(--text)] mb-2">Thanks — request received!</h3>
            <p className="text-sm text-[var(--text-mid)] leading-relaxed mb-6">
              We'll follow up by email with pricing shortly.
            </p>
            <div className="flex justify-end">
              <button
                onClick={onClose}
                className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--gold)] text-[var(--cream)] hover:opacity-90 transition-colors"
              >
                Close
              </button>
            </div>
          </>
        ) : (
          <form onSubmit={e => void submit(e)}>
            <h3 className="font-serif text-lg font-bold text-[var(--text)] mb-2">Request a Quote</h3>
            <p className="text-sm text-[var(--text-mid)] leading-relaxed mb-4">
              {reason && REASON_COPY[reason]
                ? `Looks like ${REASON_COPY[reason]}. Tell us a bit about your team and we'll get back to you with pricing.`
                : "Tell us a bit about your team and we'll get back to you with pricing."}
            </p>
            <div className="space-y-3 mb-4">
              <input
                ref={firstFieldRef}
                type="text"
                required
                placeholder="Your name"
                value={name}
                onChange={e => setName(e.target.value)}
                className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--paper)] text-sm text-[var(--text)]"
              />
              <input
                type="email"
                required
                placeholder="Work email"
                value={email}
                onChange={e => setEmail(e.target.value)}
                className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--paper)] text-sm text-[var(--text)]"
              />
              <input
                type="text"
                placeholder="Company (optional)"
                value={company}
                onChange={e => setCompany(e.target.value)}
                className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--paper)] text-sm text-[var(--text)]"
              />
              <textarea
                placeholder="Anything else we should know? (optional)"
                value={message}
                onChange={e => setMessage(e.target.value)}
                rows={3}
                className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--paper)] text-sm text-[var(--text)] resize-none"
              />
            </div>
            {error && <p className="text-sm text-[var(--red)] mb-4">{error}</p>}
            <div className="flex gap-3 justify-end">
              <button
                type="button"
                onClick={onClose}
                className="px-4 py-2 rounded-lg text-sm font-medium border border-[var(--border)] text-[var(--text-mid)] bg-transparent hover:bg-[var(--border)]/30 transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={submitting}
                className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--gold)] text-[var(--cream)] hover:opacity-90 transition-colors disabled:opacity-60"
              >
                {submitting ? 'Sending…' : 'Send Request'}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
