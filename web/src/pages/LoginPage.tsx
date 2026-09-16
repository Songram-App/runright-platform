import { useState, useEffect } from 'react'
import { login, fetchSSOProviders, fetchGitHubStatus } from '../api'
import type { SSOProvider } from '../types'
import LogoMark from '../components/LogoMark'
import { useBranding } from '../contexts/BrandingContext'

// Provider icons as simple SVG components
const ProviderIcon = ({ type }: { type: string }) => {
  switch (type) {
    case 'google':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
          <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/>
          <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
          <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
          <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
        </svg>
      )
    case 'github':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/>
        </svg>
      )
    case 'azuread':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
          <path d="M0 0h11.377v11.372H0zm12.623 0H24v11.372H12.623zM0 12.623h11.377V24H0zm12.623 0H24V24H12.623z" fill="#00A4EF"/>
        </svg>
      )
    case 'okta':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 0C5.389 0 0 5.389 0 12s5.389 12 12 12 12-5.389 12-12S18.611 0 12 0zm0 18c-3.314 0-6-2.686-6-6s2.686-6 6-6 6 2.686 6 6-2.686 6-6 6z"/>
        </svg>
      )
    case 'oidc':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10"/>
          <path d="M12 6v6l4 2"/>
        </svg>
      )
    case 'saml':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
          <path d="M7 11V7a5 5 0 0110 0v4"/>
        </svg>
      )
    case 'demo':
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
          <circle cx="12" cy="7" r="4"/>
        </svg>
      )
    default:
      return (
        <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10"/>
          <path d="M12 6v6l4 2"/>
        </svg>
      )
  }
}

interface Props {
  onLogin: () => void
}

export default function LoginPage({ onLogin }: Props) {
  const [key, setKey] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [ssoProviders, setSSOProviders] = useState<SSOProvider[]>([])
  const [loadingSSO, setLoadingSSO] = useState(true)
  const [githubEnabled, setGithubEnabled] = useState(false)
  const { name: workspaceName, logoUrl } = useBranding()
  const [dark, setDark] = useState(() =>
    typeof window !== 'undefined' ? localStorage.getItem('rr-theme') === 'dark' : false
  )
  const allowEmptyInDev = import.meta.env.DEV

  // Sync dark-mode class
  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('rr-theme', dark ? 'dark' : 'light')
  }, [dark])

  // Fetch available SSO providers on mount
  useEffect(() => {
    fetchSSOProviders()
      .then(providers => setSSOProviders(providers))
      .catch(() => setSSOProviders([]))
      .finally(() => setLoadingSSO(false))
  }, [])

  // GitHub OAuth login is available whenever this instance's GitHub App is
  // configured (true for every RunRight Cloud tenant; optional self-hosted).
  useEffect(() => {
    fetchGitHubStatus()
      .then(s => setGithubEnabled(s.configured))
      .catch(() => setGithubEnabled(false))
  }, [])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await login(key || 'dev')
      onLogin()
    } catch {
      setError('Invalid API key. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  function handleSSOLogin() {
    // If single provider, go directly. Otherwise use first one (org's primary SSO)
    const provider = ssoProviders[0]
    if (provider) {
      window.location.href = provider.login_url
    }
  }

  const hasSSO = ssoProviders.length > 0

  return (
    <div className="flex items-center justify-center min-h-screen bg-[var(--cream)] px-4 py-8 font-sans relative">
      {/* Dark mode toggle */}
      <button
        onClick={() => setDark(d => !d)}
        className="absolute top-4 right-4 p-2 text-[var(--text-light)] hover:text-[var(--text)] transition-colors"
        aria-label="Toggle dark mode"
      >
        {dark ? (
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="5"/>
            <line x1="12" y1="1" x2="12" y2="3"/>
            <line x1="12" y1="21" x2="12" y2="23"/>
            <line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/>
            <line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/>
            <line x1="1" y1="12" x2="3" y2="12"/>
            <line x1="21" y1="12" x2="23" y2="12"/>
            <line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/>
            <line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>
          </svg>
        ) : (
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>
          </svg>
        )}
      </button>

      <div className="w-full max-w-sm">

        {/* Card */}
        <div className="bg-paper border border-[var(--border)] shadow-[5px_5px_0_rgba(92,58,30,.15)] px-8 py-10 sm:px-10">

          {/* Logo */}
          <div className="flex flex-col items-center mb-8">
            {logoUrl ? (
              <img src={logoUrl} alt="" className="h-9 w-9 object-contain rounded" />
            ) : (
              <LogoMark size={36} color="currentColor" />
            )}
            <div className="font-deco text-[22px] tracking-[4px] mt-2">{workspaceName.toUpperCase()}</div>
          </div>

          {/* Single SSO Button */}
          {!loadingSSO && hasSSO && (
            <button
              type="button"
              onClick={handleSSOLogin}
              className="w-full flex items-center justify-center gap-3 py-3.5 px-4 bg-[#2C1A0E] dark:bg-[#F5E4C8] text-[#FBF0DC] dark:text-[#2C1A0E] hover:bg-[#3D2810] dark:hover:bg-[#E8D4B8] transition-colors font-deco text-[15px] tracking-[2px] mb-3"
            >
              <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
                <path d="M7 11V7a5 5 0 0110 0v4"/>
              </svg>
              <span>Sign in with SSO</span>
            </button>
          )}

          {/* GitHub OAuth login */}
          {githubEnabled && (
            <a
              href="/api/v1/github/login"
              className="w-full flex items-center justify-center gap-3 py-3.5 px-4 bg-[#2C1A0E] dark:bg-[#F5E4C8] text-[#FBF0DC] dark:text-[#2C1A0E] hover:bg-[#3D2810] dark:hover:bg-[#E8D4B8] transition-colors font-deco text-[15px] tracking-[2px] mb-3"
            >
              <ProviderIcon type="github" />
              <span>Sign in with GitHub</span>
            </a>
          )}

          {(hasSSO || githubEnabled) && (
            <div className="flex items-center gap-3 mb-5">
              <div className="flex-1 h-px bg-[var(--border)]" />
              <span className="font-deco text-[10px] tracking-[2px] text-[var(--text-light)]">OR USE API KEY</span>
              <div className="flex-1 h-px bg-[var(--border)]" />
            </div>
          )}

          {/* API Key Login */}
          <form onSubmit={handleSubmit}>
            <div className="mb-5">
              <label className="block font-deco text-[12px] tracking-[1.5px] text-[var(--text-mid)] mb-2 uppercase">
                API Key
              </label>
              <input
                type="password"
                className="rr-input !bg-[#FBF0DC] dark:!bg-[#21160E]"
                placeholder="Enter your RUNRIGHT_API_KEY"
                value={key}
                onChange={e => setKey(e.target.value)}
                autoFocus={!hasSSO}
                autoComplete="current-password"
              />
            </div>

            {error && (
              <p className="text-[13px] text-red mb-4 leading-relaxed">{error}</p>
            )}

            <button
              type="submit"
              disabled={loading || (!allowEmptyInDev && key === '')}
              className={[
                'w-full py-3 font-deco text-[16px] tracking-[2px] border-none cursor-pointer transition-all',
                (allowEmptyInDev || key) && !loading
                  ? 'bg-red text-[#FBF0DC] shadow-rr hover:bg-red-dark hover:translate-x-px hover:translate-y-px hover:shadow-[2px_2px_0_rgba(92,58,30,.2)]'
                  : 'bg-[#B8946A] dark:bg-[#5A3A18] text-[#F3E2C2] dark:text-[#C49D73] opacity-60 cursor-not-allowed',
              ].join(' ')}
            >
              {loading ? 'Signing in…' : 'Sign In'}
            </button>
          </form>
        </div>
      </div>
    </div>
  )
}
