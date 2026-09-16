import React, { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { fetchWorkspaceSettings, type WorkspaceSettings, type ThemePalette, type ThemeSettings } from '../api'

interface BrandingContextValue {
  name: string
  accentColor?: string
  logoUrl?: string
  theme?: ThemeSettings
  loading: boolean
  refresh: () => Promise<void>
}

const DEFAULT_NAME = 'RunRight'
const STYLE_TAG_ID = 'rr-brand-theme-overrides'

const BrandingContext = createContext<BrandingContextValue>({
  name: DEFAULT_NAME,
  loading: true,
  refresh: async () => {},
})

// Maps our theme field names to the actual CSS custom properties the rest of
// the app already reads (see App.css :root / html.dark blocks).
const PALETTE_VAR_MAP: Record<keyof ThemePalette, string> = {
  background: '--cream',
  surface: '--paper',
  text: '--text',
  sidebar_bg: '--sidebar-bg',
  sidebar_text: '--sidebar-fg',
  accent: '--gold',
}

function paletteToDeclarations(palette?: ThemePalette): string {
  if (!palette) return ''
  return (Object.keys(PALETTE_VAR_MAP) as (keyof ThemePalette)[])
    .filter((key) => palette[key])
    .map((key) => `${PALETTE_VAR_MAP[key]}: ${palette[key]};`)
    .join(' ')
}

// Builds a small stylesheet that overrides only the tokens the admin has
// customized — anything left blank falls through to the built-in vintage
// defaults already defined in App.css (same selectors, later source wins).
export function buildBrandingCSS(ws: WorkspaceSettings): string {
  const light: ThemePalette = { ...ws.theme?.light }
  if (!light.accent && ws.accent_color) light.accent = ws.accent_color // back-compat with the old accent-only save
  const dark = ws.theme?.dark
  const fontFamily = ws.theme?.font_family

  let css = ''
  const lightDecls = paletteToDeclarations(light)
  if (lightDecls) css += `:root { ${lightDecls} }\n`
  const darkDecls = paletteToDeclarations(dark)
  if (darkDecls) css += `html.dark { ${darkDecls} }\n`
  if (fontFamily) {
    css += `:root { --sans: ${fontFamily}; --serif: ${fontFamily}; --deco: ${fontFamily}; }\n`
  }
  return css
}

// Applies branding CSS to the live document immediately — used both for the
// real saved settings (on load/refresh) and for a temporary, unsaved preview
// while an admin is still editing Settings > General (see SettingsPage).
export function previewBrandingCSS(ws: WorkspaceSettings) {
  if (typeof document === 'undefined') return
  let tag = document.getElementById(STYLE_TAG_ID) as HTMLStyleElement | null
  if (!tag) {
    tag = document.createElement('style')
    tag.id = STYLE_TAG_ID
    document.head.appendChild(tag)
  }
  tag.textContent = buildBrandingCSS(ws)
}

export function BrandingProvider({ children }: { children: React.ReactNode }) {
  const [settings, setSettings] = useState<WorkspaceSettings>({ name: DEFAULT_NAME })
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const ws = await fetchWorkspaceSettings()
      setSettings(ws)
      previewBrandingCSS(ws)
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
      value={{
        name: settings.name || DEFAULT_NAME,
        accentColor: settings.theme?.light?.accent || settings.accent_color,
        logoUrl: settings.logo_url,
        theme: settings.theme,
        loading,
        refresh,
      }}
    >
      {children}
    </BrandingContext.Provider>
  )
}

export function useBranding() {
  return useContext(BrandingContext)
}
