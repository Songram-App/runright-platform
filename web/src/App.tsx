import { createContext, useContext, useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, NavLink, Link, Outlet, Navigate, useNavigate } from 'react-router-dom'
import JobsPage from './pages/JobsPage'
import JobDetailPage from './pages/JobDetailPage'
import JobGroupPage from './pages/JobGroupPage'
import CatalogPage from './pages/CatalogPage'
import SettingsPage from './pages/SettingsPage'
import AnalyticsPage from './pages/AnalyticsPage'
import PoliciesPage from './pages/PoliciesPage'
import AlertsPage from './pages/AlertsPage'
import AssistantPage from './pages/AssistantPage'
import AutoPRPage from './pages/AutoPRPage'
import GitHubAppPage from './pages/GitHubAppPage'
import LoginPage from './pages/LoginPage'
import CloudStartPage from './pages/CloudStartPage'
import CloudWaitPage from './pages/CloudWaitPage'
import ReposPage from './pages/ReposPage'
import RepoDetailPage from './pages/RepoDetailPage'
import RunHistoryPage from './pages/RunHistoryPage'
import ChatWidget from './components/ChatWidget'
import { UsageCapBanner } from './components/UsageCapBanner'
import { PageDataProvider } from './contexts/PageDataContext'
import { logout, fetchCurrentUser, fetchWorkspaceSettings } from './api'
import type { CurrentUser } from './types'
import LogoMark from './components/LogoMark'
import './App.css'

// ── Permissions context ──────────────────────────────────────────────────────
// Provides role + permissions to any component in the tree.
// `can(perm)` returns true if the current user holds that permission or is owner.
interface UserContextValue {
  user: CurrentUser | null
  can: (perm: string) => boolean
}

export const UserContext = createContext<UserContextValue>({
  user: null,
  can: () => true, // default: allow everything (pre-load or dev mode)
})

export const useUser = () => useContext(UserContext)

export default function App() {
  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  )
}

function AppRoutes() {
  const [authed, setAuthed] = useState(() => {
    if (typeof window === 'undefined') return false
    // Dev UX: skip the login gate by default when running the Vite dev server.
    if (import.meta.env.DEV) return true
    return localStorage.getItem('rr-auth') === 'true'
  })
  // localStorage is per-origin and won't already say "true" right after a
  // fresh claim/cross-instance login handoff, even though a valid HttpOnly
  // session cookie exists. Verify against the server once before committing
  // to the login screen, instead of trusting a stale/absent local flag.
  const [checkingSession, setCheckingSession] = useState(() => {
    if (typeof window === 'undefined' || import.meta.env.DEV) return false
    return localStorage.getItem('rr-auth') !== 'true'
  })
  const [currentUser, setCurrentUser] = useState<CurrentUser | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    if (authed) { setCheckingSession(false); return }
    fetchCurrentUser()
      .then(user => { setCurrentUser(user); setAuthed(true) })
      .catch(() => { /* no valid session — show login */ })
      .finally(() => setCheckingSession(false))
    // Only run once on mount; `authed` flips this effect off via the guard above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Load /me whenever authed state turns on.
  useEffect(() => {
    if (!authed) { setCurrentUser(null); return }
    fetchCurrentUser()
      .then(setCurrentUser)
      .catch(() => { /* ignore — owner default applies */ })
  }, [authed])

  useEffect(() => {
    localStorage.setItem('rr-auth', authed ? 'true' : 'false')
  }, [authed])

  async function handleLogout() {
    try { await logout() } catch { /* ignore */ }
    setAuthed(false)
    setCurrentUser(null)
    navigate('/')
  }

  function can(perm: string): boolean {
    if (!currentUser) return true // pre-load: optimistic allow
    const perms = currentUser.permissions
    return perms.includes('*') || perms.includes(perm)
  }

  if (checkingSession) return null

  return (
    <UserContext.Provider value={{ user: currentUser, can }}>
      <Routes>
        <Route path="/" element={<Navigate to="/login" replace />} />
        <Route path="/cloud/start" element={<CloudStartPage />} />
        <Route path="/cloud/wait" element={<CloudWaitPage />} />
        <Route
          path="/login"
          element={
            authed ? <Navigate to="/app" replace /> : (
              <LoginPage
                onLogin={() => { setAuthed(true); navigate('/app', { replace: true }) }}
              />
            )
          }
        />
        <Route
          path="/app"
          element={authed ? <AppShell onLogout={handleLogout} /> : <Navigate to="/login" replace />}
        >
          <Route index element={<JobsPage />} />
          <Route path="jobs/group/:jobId" element={<JobGroupPage />} />
          <Route path="jobs/:id" element={<JobDetailPage />} />
          <Route path="catalog" element={<CatalogPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="analytics" element={<AnalyticsPage />} />
          <Route path="history" element={<RunHistoryPage />} />
          <Route path="alerts" element={<AlertsPage />} />
          <Route path="policies" element={<PoliciesPage />} />
          <Route path="repos" element={<ReposPage />} />
          <Route path="repos/detail" element={<RepoDetailPage />} />
          <Route path="assistant" element={<AssistantPage />} />
          <Route path="auto-pr" element={<AutoPRPage />} />
          <Route path="github" element={<GitHubAppPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    </UserContext.Provider>
  )
}

// ── Sun / Moon icons ────────────────────────────────────────
const SunIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="12" cy="12" r="5"/>
    <line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/>
    <line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/>
    <line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/>
    <line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>
  </svg>
)
const MoonIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>
  </svg>
)
const MenuIcon = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
    <line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/>
  </svg>
)
const CloseIcon = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
    <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
  </svg>
)
const SignOutIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
    <polyline points="16 17 21 12 16 7"/>
    <line x1="21" y1="12" x2="9" y2="12"/>
  </svg>
)
const ChevronLeftIcon = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <polyline points="15 18 9 12 15 6"/>
  </svg>
)
const ChevronRightIcon = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <polyline points="9 18 15 12 9 6"/>
  </svg>
)

type NavIconProps = { className?: string }

const JobsIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M3.5 6.5h17"/>
    <path d="M7 3.5v6"/>
    <path d="M17 3.5v6"/>
    <rect x="3.5" y="6.5" width="17" height="14" rx="2.5"/>
    <path d="M7.5 11h4"/>
    <path d="M7.5 15h7"/>
  </svg>
)

const ReposIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M3.5 8.5A2.5 2.5 0 0 1 6 6h4l1.8 2h6.2A2.5 2.5 0 0 1 20.5 10.5v7A2.5 2.5 0 0 1 18 20H6a2.5 2.5 0 0 1-2.5-2.5z"/>
    <path d="M3.5 12h17"/>
  </svg>
)

const CatalogIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M5 6.5h14"/>
    <path d="M5 12h10"/>
    <path d="M5 17.5h14"/>
    <circle cx="17.5" cy="12" r="1.5"/>
  </svg>
)

const PolicyIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3.5 18.5 7v5.1c0 4.6-2.8 7.5-6.5 8.4-3.7-.9-6.5-3.8-6.5-8.4V7z"/>
    <path d="M9.2 12.3 11 14l3.8-3.8"/>
  </svg>
)

const AlertsIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 4.5a5.5 5.5 0 0 0-5.5 5.5v2.9c0 .6-.2 1.1-.6 1.6L4.5 16h15l-1.4-1.5c-.4-.4-.6-1-.6-1.6V10A5.5 5.5 0 0 0 12 4.5Z"/>
    <path d="M9.7 18a2.3 2.3 0 0 0 4.6 0"/>
  </svg>
)

const AnalyticsIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M3 3v18h18"/>
    <path d="M18 17V9"/>
    <path d="M13 17V5"/>
    <path d="M8 17v-3"/>
  </svg>
)

const HistoryIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="12" cy="12" r="9"/>
    <polyline points="12 7 12 12 15 15"/>
    <path d="M3 12h1"/>
    <path d="M20 12h1"/>
  </svg>
)

const SettingsIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="12" cy="12" r="3"/>
    <path d="M19.4 15a1 1 0 0 0 .2 1.1l.1.1a2 2 0 0 1-2.8 2.8l-.1-.1a1 1 0 0 0-1.1-.2 1 1 0 0 0-.6.9V20a2 2 0 1 1-4 0v-.2a1 1 0 0 0-.7-.9 1 1 0 0 0-1.1.2l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1 1 0 0 0 .2-1.1 1 1 0 0 0-.9-.6H4a2 2 0 1 1 0-4h.2a1 1 0 0 0 .9-.7 1 1 0 0 0-.2-1.1l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1 1 0 0 0 1.1.2h.1a1 1 0 0 0 .6-.9V4a2 2 0 1 1 4 0v.2a1 1 0 0 0 .7.9 1 1 0 0 0 1.1-.2l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1 1 0 0 0-.2 1.1v.1a1 1 0 0 0 .9.6H20a2 2 0 1 1 0 4h-.2a1 1 0 0 0-.9.7z"/>
  </svg>
)

const AssistantIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {/* Sparkles / AI stars */}
    <path d="M12 2l1.5 4.5L18 8l-4.5 1.5L12 14l-1.5-4.5L6 8l4.5-1.5L12 2z"/>
    <path d="M5 16l1 3 3 1-3 1-1 3-1-3-3-1 3-1 1-3z"/>
    <path d="M19 14l.75 2.25L22 17l-2.25.75L19 20l-.75-2.25L16 17l2.25-.75L19 14z"/>
  </svg>
)

const AutoPRIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {/* Git PR merge icon */}
    <circle cx="6" cy="6" r="3"/>
    <circle cx="6" cy="18" r="3"/>
    <path d="M6 9v6"/>
    <circle cx="18" cy="18" r="3"/>
    <path d="M18 9a9 9 0 0 0-9 9"/>
    <path d="M18 3v6"/>
    <path d="M15 6h6"/>
  </svg>
)

const GitHubIcon = ({ className }: NavIconProps) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"/>
  </svg>
)

// Sidebar nav link helper
function SideLink({
  to,
  end,
  onClick,
  collapsed,
  icon: Icon,
  children,
}: {
  to: string
  end?: boolean
  onClick?: () => void
  collapsed?: boolean
  icon: (props: NavIconProps) => React.ReactElement
  children: React.ReactNode
}) {
  return (
    <NavLink
      to={to}
      end={end}
      onClick={onClick}
      className={({ isActive }) =>
        [
          'group flex items-center gap-2.5 px-3 py-2.5 rounded-md text-sm font-deco tracking-widest transition-colors border border-transparent',
          collapsed ? 'md:justify-center md:px-2' : '',
          'text-[var(--sidebar-fg-muted)] hover:text-[var(--sidebar-fg)] hover:bg-[var(--sidebar-hover-bg)] hover:border-[var(--sidebar-border)]',
          isActive ? '!text-[var(--sidebar-fg)] !border-[rgba(232,196,88,.32)] !bg-[rgba(232,196,88,.10)]' : '',
        ].join(' ')
      }
    >
      {({ isActive }) => (
        <>
          <span
            className={[
              'inline-flex h-5 w-5 items-center justify-center transition-colors',
              isActive
                ? 'text-gold-light'
                : 'text-[var(--sidebar-fg-soft)] group-hover:text-[var(--sidebar-fg-muted)]',
            ].join(' ')}
          >
            <Icon />
          </span>
          <span className={collapsed ? 'md:hidden' : ''}>{children}</span>
        </>
      )}
    </NavLink>
  )
}

// Sidebar action button
function SideBtn({
  onClick,
  danger,
  collapsed,
  children,
}: {
  onClick: () => void
  danger?: boolean
  collapsed?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={[
        'flex items-center gap-2 px-3 py-2.5 w-full text-left text-sm font-deco tracking-widest transition-colors',
        collapsed ? 'md:justify-center md:px-2' : '',
        'text-[var(--sidebar-fg-soft)] hover:text-[var(--sidebar-fg-muted)]',
        danger ? 'hover:!text-red' : '',
      ].join(' ')}
    >
      {children}
    </button>
  )
}

function AppShell({ onLogout }: { onLogout: () => void }) {
  const [dark, setDark] = useState(() =>
    typeof window !== 'undefined' ? localStorage.getItem('rr-theme') === 'dark' : false
  )
  const [desktopCollapsed, setDesktopCollapsed] = useState(() =>
    typeof window !== 'undefined' ? localStorage.getItem('rr-sidebar') === 'collapsed' : false
  )
  // On desktop the sidebar is always "open"; on mobile it's a drawer.
  const [mobileOpen, setMobileOpen] = useState(false)
  const [workspaceName, setWorkspaceName] = useState('RUNRIGHT')

  useEffect(() => {
    fetchWorkspaceSettings()
      .then(ws => { if (ws.name) setWorkspaceName(ws.name.toUpperCase()) })
      .catch(() => { /* keep default branding */ })
  }, [])

  // Sync dark-mode class to <html> so Tailwind dark: variants work
  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('rr-theme', dark ? 'dark' : 'light')
  }, [dark])

  useEffect(() => {
    localStorage.setItem('rr-sidebar', desktopCollapsed ? 'collapsed' : 'expanded')
  }, [desktopCollapsed])

  // Close mobile sidebar on route change
  const closeMobile = () => setMobileOpen(false)

  return (
    <PageDataProvider>
    <div className="min-h-screen overflow-x-hidden bg-[var(--cream)] text-[var(--text)] md:flex md:h-[100dvh] md:overflow-hidden">

      {/* ── Sidebar ── */}
      {/* Mobile overlay */}
      {mobileOpen && (
        <div
          className="fixed inset-0 bg-black/45 z-40 md:hidden"
          onClick={closeMobile}
          aria-hidden="true"
        />
      )}

      <nav
        className={[
          // Base styles
          'fixed inset-y-0 left-0 z-50 flex flex-col gap-1 overflow-hidden transition-transform duration-[280ms] ease-[cubic-bezier(.4,0,.2,1)]',
          'bg-[var(--sidebar-bg)] pt-7 px-5 pb-5',
          // Mobile: slide in as drawer
          'w-[220px]',
          // On mobile, translate off-screen unless open
          mobileOpen ? 'translate-x-0' : '-translate-x-full',
          // On md+ always shown, no translate
          'md:sticky md:top-0 md:translate-x-0 md:flex-shrink-0 md:h-[100dvh]',
          desktopCollapsed ? 'md:w-[84px] md:px-3' : 'md:w-[220px] md:px-5',
          'md:transition-[width]',
        ].join(' ')}
        style={{ minWidth: 0 }}
      >
        <button
          type="button"
          onClick={() => setDesktopCollapsed((v) => !v)}
          className="hidden md:flex items-center justify-center absolute top-4 right-2 h-7 w-7 rounded-md border border-[var(--sidebar-border)] bg-[rgba(255,255,255,.03)] text-[var(--sidebar-fg-muted)] hover:text-[var(--sidebar-fg)] hover:bg-[var(--sidebar-hover-bg)] hover:border-[rgba(232,196,88,.35)] transition-colors"
          title={desktopCollapsed ? 'Expand navigation' : 'Collapse navigation'}
          aria-label={desktopCollapsed ? 'Expand navigation' : 'Collapse navigation'}
        >
          {desktopCollapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}
        </button>

        <Link
          to="/app"
          onClick={closeMobile}
          className="flex flex-col items-start gap-0.5 pb-6 border-b border-[var(--sidebar-border)] mb-5 no-underline"
          aria-label="RunRight home"
        >
          <LogoMark size={22} color="#FBF0DC" />
          <span className={["font-deco text-[22px] text-[var(--sidebar-fg)] tracking-[3px] leading-tight", desktopCollapsed ? 'md:hidden' : ''].join(' ')}>{workspaceName}</span>
        </Link>

        <SideLink to="/app" end onClick={closeMobile} collapsed={desktopCollapsed} icon={JobsIcon}>Jobs</SideLink>
        <SideLink to="/app/repos" onClick={closeMobile} collapsed={desktopCollapsed} icon={ReposIcon}>Repos</SideLink>
        <SideLink to="/app/catalog" onClick={closeMobile} collapsed={desktopCollapsed} icon={CatalogIcon}>Catalog</SideLink>
        <SideLink to="/app/policies" onClick={closeMobile} collapsed={desktopCollapsed} icon={PolicyIcon}>Policy</SideLink>
        <SideLink to="/app/alerts" onClick={closeMobile} collapsed={desktopCollapsed} icon={AlertsIcon}>Alerts</SideLink>
        <SideLink to="/app/analytics" onClick={closeMobile} collapsed={desktopCollapsed} icon={AnalyticsIcon}>Analytics</SideLink>
        <SideLink to="/app/history" onClick={closeMobile} collapsed={desktopCollapsed} icon={HistoryIcon}>Activity</SideLink>
        <SideLink to="/app/assistant" onClick={closeMobile} collapsed={desktopCollapsed} icon={AssistantIcon}>AI Assistant</SideLink>
        <SideLink to="/app/auto-pr" onClick={closeMobile} collapsed={desktopCollapsed} icon={AutoPRIcon}>Auto PR</SideLink>
        <SideLink to="/app/github" onClick={closeMobile} collapsed={desktopCollapsed} icon={GitHubIcon}>GitHub App</SideLink>
        <SideLink to="/app/settings" onClick={closeMobile} collapsed={desktopCollapsed} icon={SettingsIcon}>Settings</SideLink>

        <div className="mt-auto flex flex-col pt-4 border-t border-[var(--sidebar-border)]">
          <SideBtn onClick={() => setDark(d => !d)} collapsed={desktopCollapsed}>
            {dark ? <SunIcon /> : <MoonIcon />}
            <span className={desktopCollapsed ? 'md:hidden' : ''}>{dark ? 'Light' : 'Dark'}</span>
          </SideBtn>
          <SideBtn onClick={onLogout} collapsed={desktopCollapsed} danger>
            <SignOutIcon />
            <span className={desktopCollapsed ? 'md:hidden' : ''}>Sign Out</span>
          </SideBtn>
        </div>
      </nav>

      {/* ── Main content ── */}
      <main className="min-w-0 px-4 py-4 pt-16 bg-[var(--cream)] relative md:flex-1 md:h-[100dvh] md:px-9 md:py-10 md:overflow-y-auto">
        {/* Mobile top bar */}
        <div className="md:hidden fixed top-0 left-0 right-0 z-30 flex items-center gap-3 px-4 py-3 bg-[var(--cream)] border-b border-[var(--border)] shadow-sm">
          <button
            onClick={() => setMobileOpen(o => !o)}
            className="p-1 text-[var(--text-mid)] hover:text-[var(--text)] transition-colors"
            aria-label="Open navigation"
          >
            {mobileOpen ? <CloseIcon /> : <MenuIcon />}
          </button>
          <span className="font-deco text-lg tracking-[2px] text-[var(--text)]">{workspaceName}</span>
        </div>

        <UsageCapBanner />
        <Outlet />
      </main>

      {/* Floating AI Chat Widget */}
      <ChatWidget />
    </div>
    </PageDataProvider>
  )
}
