import { useState, useEffect, type ReactNode } from 'react'
import { fetchUserSettings, upsertUserSettings, fetchSSOConfigs, upsertSSOConfig, deleteSSOConfig, testSSOConfig, fetchUsers, updateUserRole, fetchRoles, createRole, updateRole, deleteRole, fetchWorkspaceSettings, updateWorkspaceSettings, fetchUsage, fetchAISettings, updateAISettings, deleteAISettings, type UserSettings, type UsageSummary, type AISettings, type ThemePalette } from '../api'
import { RequestQuoteModal } from '../components/RequestQuoteModal'
import { CURRENCY_OPTIONS, type CurrencyCode, useCurrencyPreference } from '../currency'
import type { SSOConfig, SSOProviderType, SSOUser, Role } from '../types'
import { useUser } from '../App'
import { useBranding, previewBrandingCSS } from '../contexts/BrandingContext'
import { usePagination } from '../hooks/usePagination'
import { ListControls } from '../components/ListControls'

// Types
interface APIKey {
  id: string
  name: string
  key_prefix: string
  scopes: string[]
  last_used_at?: string
  expires_at?: string
  created_at: string
}

interface Team {
  id: string
  name: string
  slug: string
  member_count: number
  plan: string
}

interface AuditLog {
  id: string
  actor_email: string
  action: string
  resource_type: string
  resource_name?: string
  status: string
  created_at: string
}

interface TeamMember {
  id: string
  user_email: string
  role: string
  joined_at?: string
  invited_at?: string
}

interface TeamBilling {
  plan: string
  plan_name: string
  subscription_status: string
  cancel_at_period_end: boolean
  member_count: number
  max_members: number
  billing_enabled: boolean
  has_payment_method: boolean
  current_period_end?: string
}

// Tab IDs
type TabId = 'general' | 'sso' | 'api-keys' | 'team' | 'users' | 'roles' | 'audit'

// Settings Tab Content Components
const PROVIDER_OPTIONS: { value: SSOProviderType; label: string; description: string }[] = [
  { value: 'google', label: 'Google', description: 'Google Workspace / Gmail' },
  { value: 'github', label: 'GitHub', description: 'GitHub OAuth' },
  { value: 'azuread', label: 'Azure AD', description: 'Microsoft Entra ID' },
  { value: 'okta', label: 'Okta', description: 'Okta OIDC' },
  { value: 'oidc', label: 'OIDC', description: 'Generic OpenID Connect' },
  { value: 'saml', label: 'SAML', description: 'Enterprise SAML 2.0' },
]

export default function SettingsPage() {
  const { can } = useUser()
  const [activeTab, setActiveTab] = useState<TabId>('general')

  const tabs: { id: TabId; label: string; icon: ReactNode }[] = [
    { id: 'general', label: 'General', icon: <SettingsIcon /> },
    { id: 'sso', label: 'SSO', icon: <LockIcon /> },
    { id: 'api-keys', label: 'API Keys', icon: <KeyIcon /> },
    { id: 'team', label: 'Team', icon: <UsersIcon /> },
    { id: 'users', label: 'Users & Roles', icon: <UsersIcon /> },
    { id: 'roles', label: 'Roles & Permissions', icon: <ClipboardIcon /> },
    ...(can('audit:view') ? [{ id: 'audit' as TabId, label: 'Audit Log', icon: <ClipboardIcon /> }] : []),
  ]

  return (
    <div className="fadein max-w-5xl">
      <h1 className="font-serif text-2xl sm:text-3xl font-black text-[var(--text)] mb-6 tracking-tight">
        Settings
      </h1>

      {/* Tab Navigation */}
      <div className="flex gap-1 border-b border-[var(--border)] mb-6 overflow-x-auto">
        {tabs.map(tab => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`flex items-center gap-2 px-4 py-3 text-sm font-medium transition-colors whitespace-nowrap border-b-2 -mb-px ${
              activeTab === tab.id
                ? 'border-[var(--red)] text-[var(--text)]'
                : 'border-transparent text-[var(--text-light)] hover:text-[var(--text-mid)]'
            }`}
          >
            <span className="w-4 h-4">{tab.icon}</span>
            {tab.label}
          </button>
        ))}
      </div>

      {/* Tab Content */}
      <div className="animate-fadeIn">
        {activeTab === 'general' && <GeneralTab />}
        {activeTab === 'sso' && <SSOTab />}
        {activeTab === 'api-keys' && <APIKeysTab />}
        {activeTab === 'team' && <TeamTab />}
        {activeTab === 'users' && <UsersTab />}
        {activeTab === 'roles' && <RolesTab />}
        {activeTab === 'audit' && (
          can('audit:view')
            ? <AuditTab />
            : <div className="py-12 text-center text-[var(--text-light)] text-sm">You do not have permission to view the audit log.</div>
        )}
      </div>
    </div>
  )
}

// Built-in vintage theme defaults (see web/src/App.css :root / html.dark) —
// used as color-picker placeholders and as preview fallbacks when a field is
// left blank (blank means "don't override the built-in default").
const LIGHT_DEFAULTS: Required<ThemePalette> = {
  background: '#FBF0DC', surface: '#FFFDF7', text: '#2C1A0E',
  sidebar_bg: '#2C1A0E', sidebar_text: '#FBF0DC', accent: '#B8860B',
}
const DARK_DEFAULTS: Required<ThemePalette> = {
  background: '#130C05', surface: '#231810', text: '#F5E4C8',
  sidebar_bg: '#231810', sidebar_text: '#F9E9CD', accent: '#B8860B',
}

const FONT_PRESETS: { label: string; value: string }[] = [
  { label: 'Default (Vintage)', value: '' },
  { label: 'Inter', value: 'Inter, system-ui, sans-serif' },
  { label: 'Roboto', value: 'Roboto, system-ui, sans-serif' },
  { label: 'System UI', value: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
  { label: 'Georgia (Serif)', value: 'Georgia, "Times New Roman", serif' },
  { label: 'Times New Roman', value: '"Times New Roman", Times, serif' },
  { label: 'Verdana', value: 'Verdana, Geneva, sans-serif' },
  { label: 'Courier New (Mono)', value: '"Courier New", Courier, monospace' },
]
const CUSTOM_FONT_VALUE = '__custom__'

// === General Settings Tab ===
function GeneralTab() {
  const { can } = useUser()
  const { refresh: refreshBranding } = useBranding()
  const { currency, setCurrency } = useCurrencyPreference()
  const [settings, setSettings] = useState<UserSettings>({
    otel_endpoint: '',
    allowed_machine_ids: [],
    allowed_series: [],
    allowed_families: [],
  })
  const [preferredCurrency, setPreferredCurrency] = useState<CurrencyCode>(currency)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const [workspaceName, setWorkspaceName] = useState('')
  const [lightTheme, setLightTheme] = useState<ThemePalette>({})
  const [darkTheme, setDarkTheme] = useState<ThemePalette>({})
  const [fontFamily, setFontFamily] = useState('')
  const [customFont, setCustomFont] = useState(false)
  const [themeLoaded, setThemeLoaded] = useState(false)
  const [logoDataUrl, setLogoDataUrl] = useState('')
  const [logoError, setLogoError] = useState('')
  const [workspaceSaved, setWorkspaceSaved] = useState(false)
  const [workspaceError, setWorkspaceError] = useState('')

  const [usage, setUsage] = useState<UsageSummary | null>(null)
  const [quoteOpen, setQuoteOpen] = useState(false)

  const [aiSettings, setAiSettings] = useState<AISettings | null>(null)
  const [aiProvider, setAiProvider] = useState<'' | 'openai' | 'anthropic' | 'ollama'>('')
  const [aiApiKey, setAiApiKey] = useState('')
  const [aiBaseURL, setAiBaseURL] = useState('')
  const [aiModel, setAiModel] = useState('')
  const [aiSaved, setAiSaved] = useState(false)
  const [aiError, setAiError] = useState('')

  useEffect(() => {
    void (async () => {
      try {
        const data = await fetchUserSettings()
        setSettings(data)
      } catch {
        setError('Unable to load settings.')
      } finally {
        setLoading(false)
      }
    })()
    fetchWorkspaceSettings()
      .then(ws => {
        setWorkspaceName(ws.name)
        setLogoDataUrl(ws.logo_url ?? '')
        const light = { ...ws.theme?.light }
        if (!light.accent && ws.accent_color) light.accent = ws.accent_color // migrate the old single-field save
        setLightTheme(light)
        setDarkTheme(ws.theme?.dark ?? {})
        const loadedFont = ws.theme?.font_family ?? ''
        setFontFamily(loadedFont)
        setCustomFont(loadedFont !== '' && !FONT_PRESETS.some(p => p.value === loadedFont))
      })
      .catch(() => { /* fall back to placeholder */ })
      .finally(() => setThemeLoaded(true))
    fetchUsage()
      .then(setUsage)
      .catch(() => { /* self-hosted or transient error — just hide the card */ })
    if (can('team:manage')) {
      fetchAISettings()
        .then(ai => {
          setAiSettings(ai)
          setAiProvider(ai.provider || '')
          setAiBaseURL(ai.base_url ?? '')
          setAiModel(ai.model ?? '')
        })
        .catch(() => { /* leave the card blank on error */ })
    }
  }, [])

  useEffect(() => {
    setPreferredCurrency(currency)
  }, [currency])

  // Live preview: apply pending (unsaved) theme changes to the whole app
  // immediately, so color/font pickers show their real effect right away.
  useEffect(() => {
    if (!themeLoaded) return
    previewBrandingCSS({ name: workspaceName, theme: { light: lightTheme, dark: darkTheme, font_family: fontFamily } })
  }, [themeLoaded, workspaceName, lightTheme, darkTheme, fontFamily])

  // Revert to the actually-saved theme if the admin navigates away (or
  // switches tabs) without clicking Save.
  useEffect(() => {
    return () => { void refreshBranding() }
  }, [refreshBranding])

  async function save(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    try {
      await upsertUserSettings(settings)
      setCurrency(preferredCurrency)
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch {
      setError('Unable to save settings.')
    }
  }

  async function saveWorkspaceName(e: React.FormEvent) {
    e.preventDefault()
    setWorkspaceError('')
    try {
      await updateWorkspaceSettings({
        name: workspaceName,
        logo_url: logoDataUrl,
        theme: { light: lightTheme, dark: darkTheme, font_family: fontFamily },
      })
      setWorkspaceSaved(true)
      setTimeout(() => setWorkspaceSaved(false), 2500)
      await refreshBranding() // applies the new theme / logo / name everywhere immediately
    } catch {
      setWorkspaceError('Unable to save workspace name.')
    }
  }

  const MAX_LOGO_BYTES = 350 * 1024

  function handleLogoFile(file: File | undefined) {
    setLogoError('')
    if (!file) return
    if (!/^image\/(png|jpeg|webp|svg\+xml)$/.test(file.type)) {
      setLogoError('Logo must be a PNG, JPEG, WebP, or SVG image.')
      return
    }
    if (file.size > MAX_LOGO_BYTES) {
      setLogoError('Logo is too large — please use an image under 350KB.')
      return
    }
    const reader = new FileReader()
    reader.onload = () => setLogoDataUrl(String(reader.result ?? ''))
    reader.onerror = () => setLogoError('Unable to read that file.')
    reader.readAsDataURL(file)
  }

  async function saveAISettings(e: React.FormEvent) {
    e.preventDefault()
    setAiError('')
    if (!aiProvider) {
      setAiError('Choose a provider.')
      return
    }
    try {
      await updateAISettings({ provider: aiProvider, api_key: aiApiKey, base_url: aiBaseURL, model: aiModel })
      setAiApiKey('')
      setAiSettings(await fetchAISettings())
      setAiSaved(true)
      setTimeout(() => setAiSaved(false), 2500)
    } catch {
      setAiError('Unable to save AI settings.')
    }
  }

  async function clearAISettings() {
    setAiError('')
    try {
      await deleteAISettings()
      setAiProvider('')
      setAiApiKey('')
      setAiBaseURL('')
      setAiModel('')
      setAiSettings(await fetchAISettings())
    } catch {
      setAiError('Unable to clear AI settings.')
    }
  }

  if (loading) return <LoadingState />

  return (
    <div className="space-y-6">
      <Card title="Workspace">
        <form onSubmit={e => void saveWorkspaceName(e)} className="space-y-5">
          <FormGroup label="Workspace Name" hint="Shown in the sidebar and on the login screen — make this instance feel like yours">
            <input
              type="text"
              className="settings-input"
              placeholder="RunRight"
              maxLength={60}
              value={workspaceName}
              disabled={!can('team:manage')}
              onChange={e => setWorkspaceName(e.target.value)}
            />
          </FormGroup>
          <FormGroup label="Font Family" hint="Applied across the whole dashboard — pick Custom… to enter your own CSS font stack">
            <select
              className="settings-select"
              value={customFont ? CUSTOM_FONT_VALUE : fontFamily}
              disabled={!can('team:manage')}
              onChange={e => {
                const v = e.target.value
                if (v === CUSTOM_FONT_VALUE) {
                  setCustomFont(true)
                } else {
                  setCustomFont(false)
                  setFontFamily(v)
                }
              }}
            >
              {FONT_PRESETS.map(p => (
                <option key={p.label} value={p.value}>{p.label}</option>
              ))}
              <option value={CUSTOM_FONT_VALUE}>Custom…</option>
            </select>
            {customFont && (
              <input
                type="text"
                className="settings-input mt-2"
                placeholder='e.g. "Fira Sans", sans-serif'
                value={fontFamily}
                disabled={!can('team:manage')}
                onChange={e => setFontFamily(e.target.value)}
              />
            )}
          </FormGroup>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
            <ThemePaletteEditor
              title="Light Mode"
              palette={lightTheme}
              onChange={setLightTheme}
              disabled={!can('team:manage')}
              defaults={LIGHT_DEFAULTS}
            />
            <ThemePaletteEditor
              title="Dark Mode"
              palette={darkTheme}
              onChange={setDarkTheme}
              disabled={!can('team:manage')}
              defaults={DARK_DEFAULTS}
            />
          </div>

          <FormGroup label="Logo" hint="PNG, JPEG, WebP, or SVG — max 350KB">
            <div className="flex items-center gap-4">
              {logoDataUrl && (
                <img src={logoDataUrl} alt="" className="h-12 w-12 object-contain rounded border border-[var(--border)] bg-[var(--paper)] p-1" />
              )}
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp,image/svg+xml"
                disabled={!can('team:manage')}
                onChange={e => handleLogoFile(e.target.files?.[0])}
                className="text-sm text-[var(--text-mid)] file:mr-3 file:px-3 file:py-1.5 file:rounded-lg file:border file:border-[var(--border)] file:bg-[var(--paper)] file:text-[var(--text)] file:text-sm file:cursor-pointer disabled:opacity-60"
              />
              {logoDataUrl && can('team:manage') && (
                <button type="button" onClick={() => setLogoDataUrl('')} className="text-sm text-red-600 dark:text-red-400 hover:underline">
                  Remove
                </button>
              )}
            </div>
            {logoError && <p className="text-xs text-[var(--red)] mt-1.5">{logoError}</p>}
          </FormGroup>

          <div>
            <p className="block text-sm font-medium text-[var(--text-mid)] mb-2">Preview</p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <BrandingPreview label="Light" name={workspaceName} palette={lightTheme} defaults={LIGHT_DEFAULTS} logoUrl={logoDataUrl} fontFamily={fontFamily} />
              <BrandingPreview label="Dark" name={workspaceName} palette={darkTheme} defaults={DARK_DEFAULTS} logoUrl={logoDataUrl} fontFamily={fontFamily} dark />
            </div>
          </div>

          {workspaceError && <ErrorMessage message={workspaceError} />}
          <div className="flex items-center gap-4">
            <button type="submit" className="settings-btn-primary" disabled={!can('team:manage')}>Save Branding</button>
            {workspaceSaved && <span className="text-sm text-green-600 dark:text-green-400">Saved!</span>}
          </div>
        </form>
      </Card>

      {usage?.metered && (
        <Card title="Usage This Month">
          <div className="space-y-4">
            <UsageBar label="CI jobs analyzed" value={usage.jobs_this_month ?? 0} max={usage.max_jobs_per_month ?? 0} />
            <UsageBar label="Connected repositories" value={usage.repos_connected ?? 0} max={usage.max_repos ?? 0} />
            <UsageBar label="Team members" value={usage.members_count ?? 0} max={usage.max_members ?? 0} />
            <p className="text-sm text-[var(--text-mid)]">
              You're on the <strong>{usage.plan_name ?? 'Free'}</strong> plan.{' '}
              <button type="button" onClick={() => setQuoteOpen(true)} className="text-[var(--gold)] font-medium hover:underline">
                Request a quote
              </button>{' '}
              to talk pricing for higher limits.
            </p>
          </div>
          <RequestQuoteModal open={quoteOpen} reason="proactive" onClose={() => setQuoteOpen(false)} />
        </Card>
      )}

      {can('team:manage') && (
        <Card title="AI Assistant">
          <form onSubmit={e => void saveAISettings(e)} className="space-y-5">
            <p className="text-sm text-[var(--text-mid)] -mt-1">
              Connect an AI provider so the dashboard's assistant can answer questions about your CI/CD costs.
              API keys are encrypted at rest and never shown again once saved.
            </p>
            <FormGroup label="Provider">
              <select
                className="settings-select"
                value={aiProvider}
                onChange={e => setAiProvider(e.target.value as typeof aiProvider)}
              >
                <option value="">Not connected</option>
                <option value="openai">OpenAI</option>
                <option value="anthropic">Anthropic</option>
                <option value="ollama">Ollama (self-hosted)</option>
              </select>
            </FormGroup>
            {aiProvider && aiProvider !== 'ollama' && (
              <FormGroup
                label="API Key"
                hint={aiSettings?.api_key_set ? 'A key is already saved — leave blank to keep it, or enter a new one to replace it.' : undefined}
              >
                <input
                  type="password"
                  className="settings-input"
                  placeholder={aiSettings?.api_key_set ? '••••••••••••••••' : 'sk-...'}
                  autoComplete="off"
                  value={aiApiKey}
                  onChange={e => setAiApiKey(e.target.value)}
                />
              </FormGroup>
            )}
            {aiProvider === 'ollama' && (
              <FormGroup label="Ollama Base URL" hint="e.g. http://localhost:11434">
                <input
                  type="text"
                  className="settings-input"
                  placeholder="http://localhost:11434"
                  value={aiBaseURL}
                  onChange={e => setAiBaseURL(e.target.value)}
                />
              </FormGroup>
            )}
            {aiProvider && (
              <FormGroup label="Model" hint="Optional — leave blank to use the provider's default">
                <input
                  type="text"
                  className="settings-input"
                  placeholder={aiProvider === 'openai' ? 'gpt-4o' : aiProvider === 'anthropic' ? 'claude-sonnet-4-20250514' : 'llama3.2'}
                  value={aiModel}
                  onChange={e => setAiModel(e.target.value)}
                />
              </FormGroup>
            )}
            {aiError && <ErrorMessage message={aiError} />}
            <div className="flex items-center gap-4">
              <button type="submit" className="settings-btn-primary">Save</button>
              {aiSettings?.provider && (
                <button type="button" onClick={() => void clearAISettings()} className="text-sm text-red-600 dark:text-red-400 hover:underline">
                  Disconnect
                </button>
              )}
              {aiSaved && <span className="text-sm text-green-600 dark:text-green-400">Saved!</span>}
            </div>
          </form>
        </Card>
      )}

      <Card title="Preferences">
        <form onSubmit={e => void save(e)} className="space-y-5">
          <FormGroup label="Display Currency" hint="Affects all dashboard money values">
            <select
              className="settings-select"
              value={preferredCurrency}
              onChange={e => setPreferredCurrency(e.target.value as CurrencyCode)}
            >
              {CURRENCY_OPTIONS.map(opt => (
                <option key={opt.code} value={opt.code}>{opt.label}</option>
              ))}
            </select>
          </FormGroup>

          <FormGroup label="OpenTelemetry Endpoint" hint="For OTLP metric export">
            <input
              type="text"
              className="settings-input"
              placeholder="http://localhost:4317"
              value={settings.otel_endpoint}
              onChange={e => setSettings({ ...settings, otel_endpoint: e.target.value })}
            />
          </FormGroup>

          {error && <ErrorMessage message={error} />}
          
          <div className="flex items-center gap-4">
            <button type="submit" className="settings-btn-primary">Save Changes</button>
            {saved && <span className="text-sm text-green-600 dark:text-green-400">Saved!</span>}
          </div>
        </form>
      </Card>
    </div>
  )
}

// === SSO Settings Tab ===
function SSOTab() {
  const { can } = useUser()
  const [configs, setConfigs] = useState<SSOConfig[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<Partial<SSOConfig> | null>(null)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    loadConfigs()
  }, [])

  async function loadConfigs() {
    try {
      const data = await fetchSSOConfigs()
      setConfigs(data)
    } catch {
      setError('Failed to load SSO configurations')
    } finally {
      setLoading(false)
    }
  }

  function startNew() {
    setTestResult(null)
    setEditing({
      provider_type: 'google',
      name: '',
      enabled: false,
      client_id: '',
      client_secret: '',
      scopes: 'email,profile',
      default_role: 'viewer',
    })
  }

  function callbackURLFor(providerType?: string): string {
    if (typeof window === 'undefined' || !providerType) return ''
    return `${window.location.origin}/api/v1/sso/callback/${providerType}`
  }

  async function copyCallbackURL() {
    try {
      await navigator.clipboard.writeText(callbackURLFor(editing?.provider_type))
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch { /* clipboard access denied — not critical, URL is still visible to select/copy manually */ }
  }

  async function handleTest() {
    if (!editing) return
    setTesting(true)
    setTestResult(null)
    try {
      const result = await testSSOConfig(editing)
      setTestResult({ ok: result.valid, message: result.valid ? (result.message ?? 'Configuration looks valid') : (result.error ?? 'Configuration is invalid') })
    } catch {
      setTestResult({ ok: false, message: 'Unable to test this configuration' })
    } finally {
      setTesting(false)
    }
  }

  async function handleSave() {
    if (!editing) return
    setSaving(true)
    try {
      await upsertSSOConfig(editing)
      await loadConfigs()
      setEditing(null)
    } catch {
      setError('Failed to save')
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(id: number) {
    if (!confirm('Delete this SSO provider?')) return
    await deleteSSOConfig(id)
    await loadConfigs()
  }

  if (loading) return <LoadingState />

  return (
    <div className="space-y-6">
      {editing ? (
        <Card title={editing.id ? 'Edit Provider' : 'New SSO Provider'}>
          <div className="space-y-5">
            <FormGroup label="Provider Type">
              <select
                className="settings-select"
                value={editing.provider_type}
                onChange={e => setEditing({ ...editing, provider_type: e.target.value as SSOProviderType })}
                disabled={!!editing.id}
              >
                {PROVIDER_OPTIONS.map(opt => (
                  <option key={opt.value} value={opt.value}>{opt.label} - {opt.description}</option>
                ))}
              </select>
            </FormGroup>

            <FormGroup
              label={editing.provider_type === 'saml' ? 'ACS / Callback URL' : 'Redirect URI'}
              hint={`Paste this into your ${PROVIDER_OPTIONS.find(p => p.value === editing.provider_type)?.label ?? 'identity'} app registration`}
            >
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  readOnly
                  value={callbackURLFor(editing.provider_type)}
                  onFocus={e => e.target.select()}
                  className="settings-input flex-1 text-[var(--text-mid)]"
                />
                <button type="button" onClick={() => void copyCallbackURL()} className="settings-btn-secondary whitespace-nowrap">
                  {copied ? 'Copied!' : 'Copy'}
                </button>
              </div>
            </FormGroup>

            <FormGroup label="Display Name">
              <input
                type="text"
                className="settings-input"
                placeholder="Company SSO"
                value={editing.name ?? ''}
                onChange={e => setEditing({ ...editing, name: e.target.value })}
              />
            </FormGroup>

            <div className="flex items-center gap-3">
              <input
                type="checkbox"
                id="sso-enabled"
                checked={editing.enabled ?? false}
                onChange={e => setEditing({ ...editing, enabled: e.target.checked })}
                className="w-4 h-4 rounded border-[var(--border)]"
              />
              <label htmlFor="sso-enabled" className="text-sm">Enable this provider</label>
            </div>

            {editing.provider_type !== 'saml' && (
              <>
                <div className="grid grid-cols-2 gap-4">
                  <FormGroup label="Client ID">
                    <input
                      type="text"
                      className="settings-input"
                      value={editing.client_id ?? ''}
                      onChange={e => setEditing({ ...editing, client_id: e.target.value })}
                    />
                  </FormGroup>
                  <FormGroup label="Client Secret">
                    <input
                      type="password"
                      className="settings-input"
                      placeholder={editing.id ? '••••••••' : ''}
                      value={editing.client_secret ?? ''}
                      onChange={e => setEditing({ ...editing, client_secret: e.target.value })}
                    />
                  </FormGroup>
                </div>

                {['okta', 'oidc'].includes(editing.provider_type ?? '') && (
                  <FormGroup label="Issuer URL">
                    <input
                      type="url"
                      className="settings-input"
                      placeholder="https://your-org.okta.com"
                      value={editing.issuer_url ?? ''}
                      onChange={e => setEditing({ ...editing, issuer_url: e.target.value })}
                    />
                  </FormGroup>
                )}
              </>
            )}

            {editing.provider_type === 'saml' && (
              <>
                <FormGroup label="IDP Metadata URL">
                  <input
                    type="url"
                    className="settings-input"
                    placeholder="https://idp.example.com/metadata"
                    value={editing.idp_metadata_url ?? ''}
                    onChange={e => setEditing({ ...editing, idp_metadata_url: e.target.value })}
                  />
                </FormGroup>
              </>
            )}

            <FormGroup label="Allowed Email Domains" hint="Comma-separated, leave blank for all">
              <input
                type="text"
                className="settings-input"
                placeholder="company.com, acme.org"
                value={editing.allowed_domains ?? ''}
                onChange={e => setEditing({ ...editing, allowed_domains: e.target.value })}
              />
            </FormGroup>

            {error && <ErrorMessage message={error} />}
            {testResult && (
              <p className={`text-sm ${testResult.ok ? 'text-green-600 dark:text-green-400' : 'text-[var(--red)]'}`}>
                {testResult.message}
              </p>
            )}

            <div className="flex gap-3 pt-2">
              <button onClick={handleSave} disabled={saving || !can('team:manage')} title={!can('team:manage') ? 'Requires admin or owner role' : undefined} className="settings-btn-primary">
                {saving ? 'Saving...' : 'Save Provider'}
              </button>
              <button onClick={() => void handleTest()} disabled={testing || !can('team:manage')} className="settings-btn-secondary">
                {testing ? 'Testing...' : 'Test Connection'}
              </button>
              <button onClick={() => setEditing(null)} className="settings-btn-secondary">
                Cancel
              </button>
            </div>
          </div>
        </Card>
      ) : (
        <Card 
          title="Identity Providers" 
          action={<button onClick={startNew} disabled={!can('team:manage')} title={!can('team:manage') ? 'Requires admin or owner role' : undefined} className="settings-btn-primary">Add Provider</button>}
        >
          {configs.length === 0 ? (
            <EmptyState 
              icon={<LockIcon />}
              message="No SSO providers configured"
              action={<button onClick={startNew} className="settings-btn-secondary">Add your first provider</button>}
            />
          ) : (
            <div className="divide-y divide-[var(--border)]">
              {configs.map(config => (
                <div key={config.id} className="py-4 flex items-center justify-between">
                  <div className="flex items-center gap-4">
                    <div className={`w-2 h-2 rounded-full ${config.enabled ? 'bg-green-500' : 'bg-gray-300 dark:bg-gray-600'}`} />
                    <div>
                      <div className="font-medium text-[var(--text)]">{config.name}</div>
                      <div className="text-sm text-[var(--text-light)]">
                        {PROVIDER_OPTIONS.find(p => p.value === config.provider_type)?.label}
                        {config.allowed_domains && ` · ${config.allowed_domains}`}
                      </div>
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <button onClick={() => { setTestResult(null); setEditing(config) }} className="text-sm text-[var(--text-mid)] hover:text-[var(--text)]">Edit</button>
                    <button onClick={() => handleDelete(config.id!)} className="text-sm text-red-500 hover:text-red-700">Delete</button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      )}
    </div>
  )
}

// === API Keys Tab ===
function APIKeysTab() {
  const { can } = useUser()
  const [keys, setKeys] = useState<APIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newKey, setNewKey] = useState<{ name: string; expiresIn: string }>({ name: '', expiresIn: '90d' })
  const [createdKey, setCreatedKey] = useState<string | null>(null)

  useEffect(() => {
    loadKeys()
  }, [])

  async function loadKeys() {
    try {
      const res = await fetch('/api/v1/api-keys', { credentials: 'include' })
      const data = await res.json()
      setKeys(data.api_keys || [])
    } catch {
      // Ignore
    } finally {
      setLoading(false)
    }
  }

  async function createKey() {
    if (!newKey.name) return
    setCreating(true)
    try {
      const res = await fetch('/api/v1/api-keys', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          name: newKey.name,
          expires_in: newKey.expiresIn,
          scopes: ['read', 'write'],
        }),
      })
      const data = await res.json()
      setCreatedKey(data.key)
      setNewKey({ name: '', expiresIn: '90d' })
      await loadKeys()
    } finally {
      setCreating(false)
    }
  }

  async function revokeKey(id: string) {
    if (!confirm('Revoke this API key? This cannot be undone.')) return
    await fetch(`/api/v1/api-keys/${id}`, { method: 'DELETE', credentials: 'include' })
    await loadKeys()
  }

  if (loading) return <LoadingState />

  return (
    <div className="space-y-6">
      {createdKey && (
        <Card title="New API Key Created" className="bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-800">
          <div className="space-y-3">
            <p className="text-sm text-green-800 dark:text-green-300">Copy this key now. It won't be shown again.</p>
            <div className="bg-[var(--paper)] border border-green-300 dark:border-green-700 rounded px-4 py-3 font-mono text-sm break-all text-[var(--text)]">
              {createdKey}
            </div>
            <button onClick={() => setCreatedKey(null)} className="settings-btn-secondary">Done</button>
          </div>
        </Card>
      )}

      <Card title="Create API Key">
        <div className="flex gap-4 items-end">
          <FormGroup label="Name" className="flex-1">
            <input
              type="text"
              className="settings-input"
              placeholder="My CI Key"
              value={newKey.name}
              onChange={e => setNewKey({ ...newKey, name: e.target.value })}
            />
          </FormGroup>
          <FormGroup label="Expires">
            <select
              className="settings-select"
              value={newKey.expiresIn}
              onChange={e => setNewKey({ ...newKey, expiresIn: e.target.value })}
            >
              <option value="30d">30 days</option>
              <option value="90d">90 days</option>
              <option value="1y">1 year</option>
              <option value="never">Never</option>
            </select>
          </FormGroup>
          <button onClick={createKey} disabled={creating || !newKey.name || !can('apikeys:manage')} title={!can('apikeys:manage') ? 'Requires admin or owner role' : undefined} className="settings-btn-primary">
            {creating ? 'Creating...' : 'Create Key'}
          </button>
        </div>
      </Card>

      <Card title="Your API Keys">
        {keys.length === 0 ? (
          <EmptyState icon={<KeyIcon />} message="No API keys yet" />
        ) : (
          <div className="divide-y divide-[var(--border)]">
            {keys.map(key => (
              <div key={key.id} className="py-4 flex items-center justify-between">
                <div>
                  <div className="font-medium text-[var(--text)]">{key.name}</div>
                  <div className="text-sm text-[var(--text-light)]">
                    {key.key_prefix}... · Created {new Date(key.created_at).toLocaleDateString()}
                    {key.last_used_at && ` · Last used ${new Date(key.last_used_at).toLocaleDateString()}`}
                  </div>
                </div>
                <button onClick={() => revokeKey(key.id)} className="text-sm text-red-500 hover:text-red-700">
                  Revoke
                </button>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}

// === Team Tab ===
function TeamTab() {
  const [teams, setTeams] = useState<Team[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newTeam, setNewTeam] = useState({ name: '' })
  const [selectedTeam, setSelectedTeam] = useState<Team | null>(null)
  const [members, setMembers] = useState<TeamMember[]>([])
  const [loadingMembers, setLoadingMembers] = useState(false)
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteRole, setInviteRole] = useState('member')
  const [inviting, setInviting] = useState(false)
  const [billing, setBilling] = useState<TeamBilling | null>(null)
  const [checkoutPlan, setCheckoutPlan] = useState<string | null>(null)
  const [portalLoading, setPortalLoading] = useState(false)
  const [billingError, setBillingError] = useState('')

  useEffect(() => {
    loadTeams()
  }, [])

  async function loadTeams() {
    try {
      const res = await fetch('/api/v1/teams', { credentials: 'include' })
      const data = await res.json()
      setTeams(data.teams || [])
    } catch {
      // Ignore
    } finally {
      setLoading(false)
    }
  }

  async function createTeam() {
    if (!newTeam.name) return
    setCreating(true)
    try {
      await fetch('/api/v1/teams', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ name: newTeam.name }),
      })
      setNewTeam({ name: '' })
      await loadTeams()
    } finally {
      setCreating(false)
    }
  }

  async function selectTeam(team: Team) {
    setSelectedTeam(team)
    setLoadingMembers(true)
    try {
      const res = await fetch(`/api/v1/teams/${team.id}/members`, { credentials: 'include' })
      const data = await res.json()
      setMembers(data.members || [])
    } catch {
      setMembers([])
    } finally {
      setLoadingMembers(false)
    }
    loadBilling(team.id)
  }

  async function loadBilling(teamId: string) {
    try {
      const res = await fetch(`/api/v1/teams/${teamId}/billing`, { credentials: 'include' })
      if (!res.ok) { setBilling(null); return }
      setBilling(await res.json())
    } catch {
      setBilling(null)
    }
  }

  async function upgradePlan(plan: string) {
    if (!selectedTeam) return
    setBillingError('')
    setCheckoutPlan(plan)
    try {
      const res = await fetch(`/api/v1/teams/${selectedTeam.id}/billing/checkout`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ plan }),
      })
      const data = await res.json()
      if (!res.ok) { setBillingError(data.error || 'Failed to start checkout'); return }
      window.location.href = data.checkout_url
    } catch {
      setBillingError('Failed to start checkout')
    } finally {
      setCheckoutPlan(null)
    }
  }

  async function manageBilling() {
    if (!selectedTeam) return
    setBillingError('')
    setPortalLoading(true)
    try {
      const res = await fetch(`/api/v1/teams/${selectedTeam.id}/billing/portal`, {
        method: 'POST',
        credentials: 'include',
      })
      const data = await res.json()
      if (!res.ok) { setBillingError(data.error || 'Failed to open billing portal'); return }
      window.location.href = data.portal_url
    } catch {
      setBillingError('Failed to open billing portal')
    } finally {
      setPortalLoading(false)
    }
  }

  async function inviteMember() {
    if (!selectedTeam || !inviteEmail) return
    setInviting(true)
    try {
      await fetch(`/api/v1/teams/${selectedTeam.id}/members/invite`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ email: inviteEmail, role: inviteRole }),
      })
      setInviteEmail('')
      // Reload members
      const res = await fetch(`/api/v1/teams/${selectedTeam.id}/members`, { credentials: 'include' })
      const data = await res.json()
      setMembers(data.members || [])
      await loadTeams() // Update member counts
    } finally {
      setInviting(false)
    }
  }

  async function removeMember(memberId: string) {
    if (!selectedTeam || !confirm('Remove this member from the team?')) return
    try {
      await fetch(`/api/v1/teams/${selectedTeam.id}/members/${memberId}`, {
        method: 'DELETE',
        credentials: 'include',
      })
      setMembers(members.filter(m => m.id !== memberId))
      await loadTeams() // Update member counts
    } catch {
      // Ignore
    }
  }

  if (loading) return <LoadingState />

  // Team detail view
  if (selectedTeam) {
    return (
      <div className="space-y-6">
        <button
          onClick={() => setSelectedTeam(null)}
          className="flex items-center gap-2 text-sm text-[var(--text-mid)] hover:text-[var(--text)]"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 19l-7-7 7-7" />
          </svg>
          Back to Teams
        </button>

        <Card title={selectedTeam.name} action={
          <span className="text-sm text-[var(--text-light)]">{selectedTeam.plan} plan</span>
        }>
          <div className="space-y-6">
            {/* Invite Member */}
            <div className="pb-4 border-b border-[var(--border)]">
              <div className="text-sm font-medium text-[var(--text)] mb-3">Invite Member</div>
              <div className="flex gap-3">
                <input
                  type="email"
                  className="settings-input flex-1"
                  placeholder="email@example.com"
                  value={inviteEmail}
                  onChange={e => setInviteEmail(e.target.value)}
                />
                <select
                  className="settings-select w-32"
                  value={inviteRole}
                  onChange={e => setInviteRole(e.target.value)}
                >
                  <option value="member">Member</option>
                  <option value="admin">Admin</option>
                  <option value="owner">Owner</option>
                </select>
                <button
                  onClick={inviteMember}
                  disabled={inviting || !inviteEmail}
                  className="settings-btn-primary"
                >
                  {inviting ? 'Inviting...' : 'Invite'}
                </button>
              </div>
            </div>

            {/* Members List */}
            <div>
              <div className="text-sm font-medium text-[var(--text)] mb-3">
                Members ({members.length})
              </div>
              {loadingMembers ? (
                <div className="text-sm text-[var(--text-light)] py-4">Loading members...</div>
              ) : members.length === 0 ? (
                <div className="text-sm text-[var(--text-light)] py-4">No members yet. Invite someone to get started.</div>
              ) : (
                <div className="divide-y divide-[var(--border)]">
                  {members.map(member => (
                    <div key={member.id} className="py-3 flex items-center justify-between">
                      <div>
                        <div className="font-medium text-[var(--text)]">{member.user_email}</div>
                        <div className="text-xs text-[var(--text-light)]">
                          {member.role} · {member.joined_at ? `joined ${new Date(member.joined_at).toLocaleDateString()}` : 'pending'}
                        </div>
                      </div>
                      <button
                        onClick={() => removeMember(member.id)}
                        className="text-xs text-red-600 hover:text-red-700"
                      >
                        Remove
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </Card>

        {/* Billing */}
        <Card title="Billing">
          {billingError && (
            <div className="mb-4 text-sm text-red-600">{billingError}</div>
          )}
          {!billing ? (
            <div className="text-sm text-[var(--text-light)] py-2">Loading billing info...</div>
          ) : !billing.billing_enabled ? (
            <div className="text-sm text-[var(--text-light)]">
              Billing isn't configured on this deployment. Everyone is on the {billing.plan_name} plan.
            </div>
          ) : (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <div>
                  <div className="font-medium text-[var(--text)]">{billing.plan_name} plan</div>
                  <div className="text-xs text-[var(--text-light)]">
                    {billing.member_count}{billing.max_members > 0 ? ` / ${billing.max_members}` : ''} members
                    {' · '}
                    {billing.subscription_status}
                    {billing.cancel_at_period_end ? ' (cancels at period end)' : ''}
                  </div>
                </div>
                {billing.has_payment_method && (
                  <button
                    onClick={manageBilling}
                    disabled={portalLoading}
                    className="settings-btn-secondary"
                  >
                    {portalLoading ? 'Opening...' : 'Manage billing'}
                  </button>
                )}
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                {(['pro', 'enterprise'] as const)
                  .filter(plan => plan !== billing.plan)
                  .map(plan => (
                    <div key={plan} className="border border-[var(--border)] rounded-lg p-4 flex items-center justify-between">
                      <div className="text-sm font-medium text-[var(--text)] capitalize">{plan}</div>
                      <button
                        onClick={() => upgradePlan(plan)}
                        disabled={checkoutPlan === plan}
                        className="settings-btn-primary text-sm"
                      >
                        {checkoutPlan === plan ? 'Redirecting...' : 'Upgrade'}
                      </button>
                    </div>
                  ))}
              </div>
            </div>
          )}
        </Card>
      </div>
    )
  }

  // Teams list view
  return (
    <div className="space-y-6">
      <Card title="Create Team">
        <div className="flex gap-4 items-end">
          <FormGroup label="Team Name" className="flex-1">
            <input
              type="text"
              className="settings-input"
              placeholder="Engineering"
              value={newTeam.name}
              onChange={e => setNewTeam({ name: e.target.value })}
            />
          </FormGroup>
          <button onClick={createTeam} disabled={creating || !newTeam.name} className="settings-btn-primary">
            {creating ? 'Creating...' : 'Create Team'}
          </button>
        </div>
      </Card>

      <Card title="Your Teams">
        {teams.length === 0 ? (
          <EmptyState icon={<UsersIcon />} message="No teams yet" />
        ) : (
          <div className="divide-y divide-[var(--border)]">
            {teams.map(team => (
              <div key={team.id} className="py-4 flex items-center justify-between">
                <div>
                  <div className="font-medium text-[var(--text)]">{team.name}</div>
                  <div className="text-sm text-[var(--text-light)]">
                    {team.member_count} member{team.member_count !== 1 ? 's' : ''} · {team.plan} plan
                  </div>
                </div>
                <button
                  onClick={() => selectTeam(team)}
                  className="text-sm text-[var(--text-mid)] hover:text-[var(--text)]"
                >
                  Manage
                </button>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}

// === Users & Roles Tab ===
function UsersTab() {
  const { can } = useUser()
  const [users, setUsers] = useState<SSOUser[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState<string | null>(null)

  // Pagination for users
  const usersPagination = usePagination({
    items: users,
    pageSize: 10,
    searchFn: (u, query) =>
      (u.name?.toLowerCase().includes(query) ?? false) ||
      u.email.toLowerCase().includes(query) ||
      u.role.toLowerCase().includes(query) ||
      u.provider.toLowerCase().includes(query),
  })

  useEffect(() => { void load() }, [])

  async function load() {
    // Load roles and users independently so a permission error on /users
    // (e.g. viewer role) doesn't prevent the role selector from populating.
    try {
      const r = await fetchRoles()
      setRoles(r.roles)
    } catch {
      // Non-critical for this tab — role selector will fall back to empty
    }
    try {
      setUsers(await fetchUsers())
    } catch {
      setError('Failed to load users. Users appear here after their first SSO login.')
    } finally {
      setLoading(false)
    }
  }

  async function changeRole(email: string, role: string) {
    setSaving(email)
    try {
      await updateUserRole(email, role)
      setUsers((prev) => prev.map((u) => u.email === email ? { ...u, role } : u))
    } catch {
      setError('Failed to update role.')
    } finally {
      setSaving(null)
    }
  }

  if (loading) return <LoadingState />

  const roleDesc = (name: string) => roles.find((r) => r.name === name)?.description ?? ''

  return (
    <div className="space-y-6">
      {error && <ErrorMessage message={error} />}
      <Card title="Users & Role Assignments">
        <p className="text-sm text-[var(--text-light)] mb-4">
          Users appear here after their first SSO login. Change their role to control what they can do in the dashboard.
        </p>
        {users.length === 0 ? (
          <EmptyState icon={<UsersIcon />} message="No SSO users yet. Users will appear here after logging in via SSO." />
        ) : (
          <>
            <ListControls
              pagination={usersPagination}
              searchPlaceholder="Search users by name, email, or role..."
              pageSizeOptions={[10, 25, 50]}
              className="mb-4"
            />
            <div className="divide-y divide-[var(--border)]">
              {usersPagination.paginatedItems.map((u) => (
                <div key={u.email} className="py-3 flex items-center gap-4 flex-wrap sm:flex-nowrap">
                  <div className="flex-1 min-w-0">
                    <div className="font-medium text-[var(--text)] truncate">{u.name || u.email}</div>
                    <div className="text-xs text-[var(--text-light)] truncate">
                      {u.email} · {u.provider}
                      {u.last_login_at && ` · Last login ${new Date(u.last_login_at).toLocaleDateString()}`}
                    </div>
                  </div>
                  <div className="flex-shrink-0 flex items-center gap-2">
                    <select
                      className="settings-select text-sm"
                      value={u.role}
                      disabled={!can('team:manage') || saving === u.email}
                      onChange={(e) => void changeRole(u.email, e.target.value)}
                      title={roleDesc(u.role)}
                    >
                      {roles.map((r) => (
                        <option key={r.name} value={r.name}>{r.name.charAt(0).toUpperCase() + r.name.slice(1)}</option>
                      ))}
                    </select>
                    {saving === u.email && <span className="text-xs text-[var(--text-light)]">Saving…</span>}
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </Card>

      <Card title="Role Reference">
        <div className="space-y-2">
          {roles.map((r) => (
            <div key={r.name} className="flex items-start gap-3 text-sm">
              <span className="font-mono font-semibold text-[var(--gold)] w-24 flex-shrink-0 capitalize">{r.name}</span>
              <span className="text-[var(--text-mid)]">{r.description || '—'}</span>
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}

// === Roles & Permissions Tab ===

const PERMISSION_LABELS: Record<string, string> = {
  'policies:manage':  'Manage cost policies',
  'alerts:manage':    'Manage alerts & notification rules',
  'jobs:manage':      'Snooze / archive / delete jobs',
  'ownership:manage': 'Manage repository ownership',
  'team:manage':      'Manage teams, SSO, roles & members',
  'apikeys:manage':   'Create / revoke API keys',
  'reports:manage':   'Create / manage scheduled reports',
  'audit:view':       'View audit logs',
}

const PERMISSION_HINTS: Record<string, string> = {
  'policies:manage':  'Create, edit and delete cost policies that govern job runs',
  'alerts:manage':    'Configure alert rules and notification destinations',
  'jobs:manage':      'Snooze, archive and delete job run history',
  'ownership:manage': 'Assign repository owners and manage ownership records',
  'team:manage':      'Invite members, configure SSO, manage roles and API keys',
  'apikeys:manage':   'Issue and revoke programmatic API keys',
  'reports:manage':   'Schedule and export cost reports',
  'audit:view':       'Read the audit log of all system activity',
}

// Fallback used when the server hasn't responded yet or failed to load.
const FALLBACK_PERMS = Object.keys(PERMISSION_LABELS)

function PermissionCheckboxes({
  value,
  onChange,
  available,
  disabled,
}: {
  value: string[]
  onChange: (perms: string[]) => void
  available: string[]
  disabled: boolean
}) {
  const perms = available.length > 0 ? available : FALLBACK_PERMS
  function toggle(perm: string) {
    if (value.includes(perm)) onChange(value.filter((p) => p !== perm))
    else onChange([...value, perm])
  }
  return (
    <div className="grid grid-cols-1 gap-y-2 mt-2">
      {perms.map((perm) => (
        <label key={perm} className={`flex items-start gap-2.5 text-sm select-none rounded px-2 py-1.5 ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer hover:bg-[var(--cream-alt)]'}`}>
          <input
            type="checkbox"
            checked={value.includes(perm)}
            disabled={disabled}
            onChange={() => toggle(perm)}
            className="accent-[var(--gold)] mt-0.5 flex-shrink-0"
          />
          <div>
            <div className="font-medium text-[var(--text)]">{PERMISSION_LABELS[perm] ?? perm}</div>
            {PERMISSION_HINTS[perm] && <div className="text-[11px] text-[var(--text-light)] mt-0.5">{PERMISSION_HINTS[perm]}</div>}
          </div>
        </label>
      ))}
    </div>
  )
}

function RolesTab() {
  const { can } = useUser()
  const canWrite = can('team:manage')

  const [roles, setRoles] = useState<Role[]>([])
  const [available, setAvailable] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [editing, setEditing] = useState<Record<string, { description: string; permissions: string[] }>>({})
  const [saving, setSaving] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [showNew, setShowNew] = useState(false)
  const [newRole, setNewRole] = useState({ name: '', description: '', permissions: [] as string[] })
  const [newError, setNewError] = useState('')

  // Pagination for roles
  const rolesPagination = usePagination({
    items: roles,
    pageSize: 10,
    searchFn: (r, query) =>
      r.name.toLowerCase().includes(query) ||
      r.description.toLowerCase().includes(query),
  })

  useEffect(() => { void load() }, [])

  async function load() {
    try {
      const r = await fetchRoles()
      setRoles(r.roles)
      setAvailable(r.available_permissions)
    } catch (e: any) {
      setError(e?.response?.data?.error ?? 'Failed to load roles. Try refreshing the page.')
    } finally {
      setLoading(false)
    }
  }

  function beginEdit(role: Role) {
    setEditing((prev) => ({
      ...prev,
      [role.id]: { description: role.description, permissions: [...role.permissions] },
    }))
    setExpandedId(role.id)
  }

  async function saveEdit(role: Role) {
    const draft = editing[role.id]
    if (!draft) return
    setSaving(role.id)
    try {
      await updateRole(role.id, draft)
      setRoles((prev) => prev.map((r) => r.id === role.id ? { ...r, ...draft } : r))
      setEditing((prev) => { const n = { ...prev }; delete n[role.id]; return n })
    } catch {
      setError('Failed to save role.')
    } finally {
      setSaving(null)
    }
  }

  async function handleDelete(role: Role) {
    if (!window.confirm(`Delete role "${role.name}"? Users assigned this role will fall back to "viewer".`)) return
    setDeleting(role.id)
    try {
      await deleteRole(role.id)
      setRoles((prev) => prev.filter((r) => r.id !== role.id))
    } catch {
      setError('Failed to delete role.')
    } finally {
      setDeleting(null)
    }
  }

  async function handleCreate() {
    setNewError('')
    if (!newRole.name.trim()) { setNewError('Name is required.'); return }
    try {
      await createRole({ name: newRole.name.trim(), description: newRole.description, permissions: newRole.permissions })
      setShowNew(false)
      setNewRole({ name: '', description: '', permissions: [] })
      await load()
    } catch (e: any) {
      setNewError(e?.response?.data?.error ?? 'Failed to create role.')
    }
  }

  if (loading) return <LoadingState />

  return (
    <div className="space-y-6">
      {error && (
        <div className="flex items-center gap-3">
          <ErrorMessage message={error} />
          <button className="btn-secondary text-xs flex-shrink-0" onClick={() => { setError(''); void load() }}>Retry</button>
        </div>
      )}

      <Card title="Roles & Permissions">
        <p className="text-sm text-[var(--text-light)] mb-4">
          System roles are built-in and cannot be deleted. You can update their permissions and description.
          Custom roles can be fully managed and assigned to users on the Users &amp; Roles tab.
        </p>

        <ListControls
          pagination={rolesPagination}
          searchPlaceholder="Search roles..."
          pageSizeOptions={[10, 25, 50]}
          className="mb-4"
        />

        <div className="divide-y divide-[var(--border)]">
          {rolesPagination.paginatedItems.map((role) => {
            const isExpanded = expandedId === role.id
            const draft = editing[role.id]
            const isDirty = !!draft

            return (
              <div key={role.id} className="py-4">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-semibold text-[var(--text)] capitalize">{role.name}</span>
                      {role.is_system && (
                        <span className="text-[10px] font-semibold uppercase tracking-wider bg-[var(--paper)] border border-[var(--border)] text-[var(--text-light)] px-1.5 py-0.5 rounded">
                          system
                        </span>
                      )}
                    </div>
                    {!isDirty && (
                      <p className="text-sm text-[var(--text-mid)] mt-0.5">{role.description || '—'}</p>
                    )}
                    {!isDirty && (
                      <div className="flex flex-wrap gap-1 mt-1">
                        {role.permissions.includes('*') ? (
                          <span className="text-xs text-[var(--text-light)] italic">All permissions (unrestricted)</span>
                        ) : role.permissions.length === 0 ? (
                          <span className="text-xs text-[var(--text-light)] italic">No permissions (read-only)</span>
                        ) : (
                          role.permissions.map((p) => (
                            <span key={p} title={PERMISSION_HINTS[p]} className="text-[11px] bg-[var(--cream-alt)] text-[var(--text-mid)] px-2 py-0.5 rounded">
                              {PERMISSION_LABELS[p] ?? p}
                            </span>
                          ))
                        )}
                      </div>
                    )}
                  </div>
                  <div className="flex items-center gap-2 flex-shrink-0">
                    {canWrite && !isDirty && (
                      <button className="btn-secondary text-xs" onClick={() => beginEdit(role)}>
                        Edit
                      </button>
                    )}
                    {canWrite && !role.is_system && !isDirty && (
                      <button
                        className="btn-danger text-xs"
                        disabled={deleting === role.id}
                        onClick={() => void handleDelete(role)}
                      >
                        {deleting === role.id ? 'Deleting…' : 'Delete'}
                      </button>
                    )}
                  </div>
                </div>

                {isDirty && (
                  <div className="mt-3 space-y-3">
                    <div>
                      <label className="block text-xs font-medium text-[var(--text-light)] mb-1">Description</label>
                      <input
                        type="text"
                        className="settings-input w-full"
                        value={draft.description}
                        onChange={(e) => setEditing((prev) => ({ ...prev, [role.id]: { ...prev[role.id], description: e.target.value } }))}
                      />
                    </div>
                    {!role.permissions.includes('*') && (
                      <div>
                        <label className="block text-xs font-medium text-[var(--text-light)] mb-1">Permissions</label>
                        <PermissionCheckboxes
                          value={draft.permissions}
                          available={available}
                          disabled={false}
                          onChange={(perms) => setEditing((prev) => ({ ...prev, [role.id]: { ...prev[role.id], permissions: perms } }))}
                        />
                      </div>
                    )}
                    {role.permissions.includes('*') && (
                      <p className="text-sm text-[var(--text-light)] italic">This role has unrestricted access ("*") — all permissions are granted.</p>
                    )}
                    <div className="flex gap-2 pt-1">
                      <button className="btn-primary text-sm" disabled={saving === role.id} onClick={() => void saveEdit(role)}>
                        {saving === role.id ? 'Saving…' : 'Save'}
                      </button>
                      <button className="btn-secondary text-sm" onClick={() => setEditing((prev) => { const n = { ...prev }; delete n[role.id]; return n })}>
                        Cancel
                      </button>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </Card>

      {canWrite && (
        <Card title={showNew ? 'New Role' : ''}>
          {!showNew ? (
            <button className="btn-primary" onClick={() => setShowNew(true)}>+ New Role</button>
          ) : (
            <div className="space-y-4">
              {newError && <ErrorMessage message={newError} />}
              <div>
                <label className="block text-xs font-medium text-[var(--text-light)] mb-1">Name <span className="text-red-500">*</span></label>
                <input
                  type="text"
                  className="settings-input w-full"
                  placeholder="e.g. security-reviewer"
                  value={newRole.name}
                  onChange={(e) => setNewRole((p) => ({ ...p, name: e.target.value }))}
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-[var(--text-light)] mb-1">Description</label>
                <input
                  type="text"
                  className="settings-input w-full"
                  placeholder="What can this role do?"
                  value={newRole.description}
                  onChange={(e) => setNewRole((p) => ({ ...p, description: e.target.value }))}
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-[var(--text-light)] mb-1">Permissions</label>
                <PermissionCheckboxes
                  value={newRole.permissions}
                  available={available}
                  disabled={false}
                  onChange={(perms) => setNewRole((p) => ({ ...p, permissions: perms }))}
                />
              </div>
              <div className="flex gap-2 pt-1">
                <button className="btn-primary" onClick={() => void handleCreate()}>Create Role</button>
                <button className="btn-secondary" onClick={() => { setShowNew(false); setNewRole({ name: '', description: '', permissions: [] }); setNewError('') }}>
                  Cancel
                </button>
              </div>
            </div>
          )}
        </Card>
      )}
    </div>
  )
}


function AuditTab() {
  const [logs, setLogs] = useState<AuditLog[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadLogs()
  }, [])

  async function loadLogs() {
    try {
      const res = await fetch('/api/v1/audit-logs?limit=50', { credentials: 'include' })
      const data = await res.json()
      setLogs(data.logs || [])
    } catch {
      // Ignore
    } finally {
      setLoading(false)
    }
  }

  if (loading) return <LoadingState />

  return (
    <Card title="Recent Activity" action={
      <button className="text-sm text-[var(--text-mid)] hover:text-[var(--text)]">Export CSV</button>
    }>
      {logs.length === 0 ? (
        <EmptyState icon={<ClipboardIcon />} message="No activity logged yet" />
      ) : (
        <div className="divide-y divide-[var(--border)]">
          {logs.map(log => (
            <div key={log.id} className="py-3 flex items-start gap-4">
              <div className={`w-2 h-2 rounded-full mt-2 ${log.status === 'success' ? 'bg-green-500' : 'bg-red-500'}`} />
              <div className="flex-1 min-w-0">
                <div className="text-sm text-[var(--text)]">
                  <span className="font-medium">{log.actor_email === 'system' ? 'System' : log.actor_email}</span>
                  {' '}{formatAuditEntry(log)}
                </div>
                <div className="text-xs text-[var(--text-light)]">
                  {new Date(log.created_at).toLocaleString()}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

// === Shared Components ===

function Card({ title, action, children, className = '' }: { title: string; action?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <div className={`bg-[var(--paper)] border border-[var(--border)] rounded-lg shadow-sm ${className}`}>
      <div className="flex items-center justify-between px-6 py-4 border-b border-[var(--border)]">
        <h2 className="font-semibold text-[var(--text)]">{title}</h2>
        {action}
      </div>
      <div className="px-6 py-5">{children}</div>
    </div>
  )
}

function FormGroup({ label, hint, children, className = '' }: { label: string; hint?: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={className}>
      <label className="block text-sm font-medium text-[var(--text-mid)] mb-1.5">{label}</label>
      {children}
      {hint && <p className="text-xs text-[var(--text-light)] mt-1.5">{hint}</p>}
    </div>
  )
}

function LoadingState() {
  return <div className="text-sm text-[var(--text-light)] py-8 text-center">Loading...</div>
}

// Renders a mini sidebar/button/background mockup using the PENDING (unsaved)
// theme values for one mode, scoped to this subtree only via inline CSS
// custom properties so it never touches the real app theme until Save.
function BrandingPreview({
  label, name, palette, defaults, logoUrl, fontFamily, dark,
}: {
  label: string
  name: string
  palette: ThemePalette
  defaults: Required<ThemePalette>
  logoUrl: string
  fontFamily: string
  dark?: boolean
}) {
  const resolved = { ...defaults, ...Object.fromEntries(Object.entries(palette).filter(([, v]) => v)) }
  const style = {
    '--gold': resolved.accent,
    background: resolved.background,
    color: resolved.text,
    fontFamily: fontFamily || undefined,
  } as React.CSSProperties
  return (
    <div className={`rounded-lg border border-[var(--border)] overflow-hidden ${dark ? 'dark' : ''}`}>
      <div className="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wide text-[var(--text-light)] bg-[var(--paper)] border-b border-[var(--border)]">
        {label}
      </div>
      <div style={style} className="p-3 space-y-3">
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg" style={{ background: resolved.sidebar_bg }}>
          {logoUrl ? (
            <img src={logoUrl} alt="" className="h-5 w-5 object-contain rounded" />
          ) : (
            <div className="h-5 w-5 rounded-full border-2" style={{ borderColor: resolved.sidebar_text }} />
          )}
          <span className="font-deco text-sm tracking-[2px]" style={{ color: resolved.sidebar_text, fontFamily: fontFamily || undefined }}>
            {(name || 'RunRight').toUpperCase()}
          </span>
        </div>
        <div className="flex items-center gap-3 px-3 py-2 rounded-lg border" style={{ borderColor: resolved.accent + '40', background: resolved.surface }}>
          <button type="button" tabIndex={-1} className="px-3 py-1.5 rounded-lg text-xs font-semibold text-white" style={{ background: resolved.accent }}>
            Button
          </button>
          <span className="text-xs">Body text</span>
        </div>
      </div>
    </div>
  )
}

// A titled block of 6 labeled color pickers (background/surface/text/sidebar
// bg/sidebar text/accent) for one theme mode. Blank = use the built-in default.
function ThemePaletteEditor({
  title, palette, onChange, disabled, defaults,
}: {
  title: string
  palette: ThemePalette
  onChange: (next: ThemePalette) => void
  disabled: boolean
  defaults: Required<ThemePalette>
}) {
  const fields: { key: keyof ThemePalette; label: string }[] = [
    { key: 'background', label: 'Background' },
    { key: 'surface', label: 'Surface (cards)' },
    { key: 'text', label: 'Text' },
    { key: 'sidebar_bg', label: 'Sidebar Background' },
    { key: 'sidebar_text', label: 'Sidebar Text' },
    { key: 'accent', label: 'Accent' },
  ]
  return (
    <div className="border border-[var(--border)] rounded-lg p-4">
      <p className="text-sm font-semibold text-[var(--text)] mb-3">{title}</p>
      <div className="space-y-2.5">
        {fields.map(({ key, label }) => (
          <div key={key} className="flex items-center gap-2">
            <input
              type="color"
              value={palette[key] || defaults[key]}
              disabled={disabled}
              onChange={e => onChange({ ...palette, [key]: e.target.value })}
              className="w-8 h-8 rounded border border-[var(--border)] bg-transparent cursor-pointer disabled:cursor-not-allowed flex-shrink-0"
            />
            <span className="text-xs text-[var(--text-mid)] flex-1">{label}</span>
            {palette[key] && !disabled && (
              <button type="button" onClick={() => onChange({ ...palette, [key]: '' })} className="text-[10px] text-[var(--text-light)] hover:text-[var(--red)] hover:underline">
                Reset
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function UsageBar({ label, value, max }: { label: string; value: number; max: number }) {
  const unlimited = max <= 0
  const pct = unlimited ? 0 : Math.min(100, Math.round((value / max) * 100))
  const atCap = !unlimited && value >= max
  return (
    <div>
      <div className="flex items-center justify-between text-xs text-[var(--text-mid)] mb-1">
        <span>{label}</span>
        <span>{unlimited ? `${value} (unlimited)` : `${value} / ${max}`}</span>
      </div>
      {!unlimited && (
        <div className="h-2 rounded-full bg-[var(--border)] overflow-hidden">
          <div
            className={`h-full rounded-full ${atCap ? 'bg-[var(--red)]' : 'bg-[var(--gold)]'}`}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </div>
  )
}

function ErrorMessage({ message }: { message: string }) {
  return <div className="text-sm text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded px-4 py-3">{message}</div>
}

function EmptyState({ icon, message, action }: { icon: React.ReactNode; message: string; action?: React.ReactNode }) {
  return (
    <div className="text-center py-8">
      <div className="w-12 h-12 mx-auto mb-4 text-[var(--border-dark)]">{icon}</div>
      <p className="text-sm text-[var(--text-light)] mb-4">{message}</p>
      {action}
    </div>
  )
}

function formatAuditEntry(log: AuditLog): string {
  const { action, resource_type, resource_name } = log
  
  // Parse action like "alert.rule.create" or "notifications.settings.update"
  const parts = action.split('.')
  const verb = parts[parts.length - 1] // last part is usually the verb
  
  // Map verbs to past tense
  const verbMap: Record<string, string> = {
    create: 'created',
    update: 'updated',
    delete: 'deleted',
    revoke: 'revoked',
    invite: 'invited',
    remove: 'removed',
    export: 'exported',
    run: 'ran',
    upsert: 'updated',
    assign: 'assigned',
    unassign: 'unassigned',
  }
  const pastVerb = verbMap[verb] || verb
  
  // Build human-readable resource type
  const typeMap: Record<string, string> = {
    'alert': 'alert rule',
    'alert_rule': 'alert rule',
    'policy': 'policy',
    'policy_rule': 'policy rule',
    'role': 'role',
    'user': 'user',
    'apikey': 'API key',
    'api_key': 'API key',
    'team': 'team',
    'team_member': 'team member',
    'sso': 'SSO configuration',
    'sso_provider': 'SSO provider',
    'notification': 'notification settings',
    'notifications': 'notification settings',
    'settings': 'settings',
    'ownership': 'repository ownership',
    'repository_ownership': 'repository ownership',
    'job': 'job',
    'label': 'label mapping',
    'label_mapping': 'label mapping',
  }
  
  // Try to determine the resource type from action parts or resource_type field
  let displayType = resource_type ? typeMap[resource_type] || resource_type : ''
  
  // If resource_type isn't helpful, parse from action
  if (!displayType || displayType === resource_type) {
    // Check for patterns like "alert.rule.create" or "notifications.settings.update"
    if (parts.length >= 2) {
      const category = parts[0]
      const subtype = parts[1]
      
      if (category === 'alert' && subtype === 'rule') {
        displayType = 'alert rule'
      } else if (category === 'notifications' && subtype === 'settings') {
        displayType = 'notification settings'
      } else if (category === 'policy' && subtype === 'rule') {
        displayType = 'policy rule'
      } else if (category === 'api' && subtype === 'key') {
        displayType = 'API key'
      } else if (category === 'repository' && subtype === 'ownership') {
        displayType = 'repository ownership'
      } else if (category === 'job' && subtype === 'meta') {
        displayType = 'job metadata'
      } else if (category === 'user' && subtype === 'role') {
        displayType = 'user role'
      } else {
        displayType = typeMap[category] || category
      }
    }
  }
  
  // Build final message
  if (resource_name) {
    return `${pastVerb} ${displayType} "${resource_name}"`
  } else if (displayType) {
    return `${pastVerb} ${displayType}`
  } else {
    return pastVerb
  }
}

// Icons
function SettingsIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  )
}

function LockIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
      <path d="M7 11V7a5 5 0 0 1 10 0v4" />
    </svg>
  )
}

function KeyIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  )
}

function UsersIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
      <path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  )
}

function ClipboardIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2" />
      <rect x="8" y="2" width="8" height="4" rx="1" ry="1" />
    </svg>
  )
}
