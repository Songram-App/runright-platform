import React, { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { fetchWorkspaceSettings, type WorkspaceSettings } from '../api'

interface BrandingContextValue {
  name: string
  accentColor?: string
  logoUrl?: string
  loading: boolean
  refresh: () => Promise<void>
}

const DEFAULT_NAME = 'RunRight'

const BrandingContext = createContext<BrandingContextValue>({
  name: DEFAULT_NAME,
  loading: true,
  refresh: async () => {},
})

// Applies the accent color to the document so it re-themes every button,
// link, and focus ring across the app (they all read var(--gold)). Reverts
// to the built-in default when no custom color is set.
function applyAccentColor(accentColor?: string) {
  if (typeof document === 'undefined') return
  if (accentColor) {
    document.documentElement.style.setProperty('--gold', accentColor)
  } else {
    document.documentElement.style.removeProperty('--gold')
  }
}

export function BrandingProvider({ children }: { children: React.ReactNode }) {
  const [settings, setSettings] = useState<WorkspaceSettings>({ name: DEFAULT_NAME })
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const ws = await fetchWorkspaceSettings()
      setSettings(ws)
      applyAccentColor(ws.accent_color)
    } catch {
      // Self-hosted/pre-migration/transient error — keep default branding.
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return (
    <BrandingContext.Provider
      value={{ name: settings.name || DEFAULT_NAME, accentColor: settings.accent_color, logoUrl: settings.logo_url, loading, refresh }}
    >
      {children}
    </BrandingContext.Provider>
  )
}

export function useBranding() {
  return useContext(BrandingContext)
}
