import axios from 'axios'
import type {
  Job, MachineType, SavingsSummary, SavingsHistoryPoint, RepoSummary, JobSummaryRow,
  PolicyRule, PolicyEvaluation, NotificationSettings, DeliveryLog, OwnershipEntry,
  SSOProvider, SSOUser, SSOConfig, CurrentUser, Role,
} from './types'

const api = axios.create({
  baseURL: '/api/v1',
  headers: { 'Content-Type': 'application/json' },
  withCredentials: true, // send the HttpOnly session cookie automatically
})

// If the backend session cookie is missing/expired (common after backend restarts),
// reset client auth state and force a clean login.
api.interceptors.response.use(
  (response) => response,
  (error) => {
    const status = error?.response?.status
    const reqURL = String(error?.config?.url ?? '')
    const isAuthEndpoint = reqURL.includes('/auth')

    if (status === 401 && !isAuthEndpoint && typeof window !== 'undefined' && !import.meta.env.DEV) {
      localStorage.setItem('rr-auth', 'false')
      if (window.location.pathname !== '/login') {
        window.location.replace('/login')
      }
    }

    return Promise.reject(error)
  },
)

export const fetchCurrentUser = (): Promise<CurrentUser> =>
  api.get<CurrentUser>('/me').then((r) => r.data)

export const login = (apiKey: string): Promise<void> =>
  api.post('/auth', { api_key: apiKey }).then(() => undefined)

export const logout = (): Promise<void> =>
  api.post('/auth/logout').then(() => undefined)

export const fetchJobs = (repository?: string): Promise<Job[]> =>
  api.get<Job[]>('/jobs', { params: repository ? { repository } : {} }).then((r) => r.data ?? [])

export const fetchJob = (id: number): Promise<Job> =>
  api.get<Job>(`/jobs/${id}`).then((r) => r.data)

export const fetchCatalog = (provider?: string): Promise<MachineType[]> =>
  api.get<MachineType[]>('/catalog', { params: provider ? { provider } : {} }).then((r) => r.data ?? [])

export const fetchSavings = (repository?: string): Promise<SavingsSummary> =>
  api.get<SavingsSummary>('/savings', { params: repository ? { repository } : {} }).then((r) => r.data)

export const fetchSavingsHistory = (): Promise<SavingsHistoryPoint[]> =>
  api.get<SavingsHistoryPoint[]>('/savings/history').then((r) => r.data ?? [])

export const fetchJobTrend = (jobId: string, window = 10): Promise<unknown> =>
  api.get(`/jobs/${encodeURIComponent(jobId)}/trend`, { params: { window } }).then((r) => r.data)

// ── Repo-centric API ─────────────────────────────────────────────────────────

export const fetchRepos = (): Promise<RepoSummary[]> =>
  api.get<RepoSummary[]>('/repos').then((r) => r.data ?? [])

export const fetchRepoJobs = (repository: string, includeArchived = false): Promise<JobSummaryRow[]> =>
  api
    .get<JobSummaryRow[]>('/repo-jobs', {
      params: { repository, ...(includeArchived ? { include_archived: 'true' } : {}) },
    })
    .then((r) => r.data ?? [])

export const fetchIsolatedJobs = (includeArchived = false): Promise<JobSummaryRow[]> =>
  api
    .get<JobSummaryRow[]>('/isolated-jobs', {
      params: includeArchived ? { include_archived: 'true' } : {},
    })
    .then((r) => r.data ?? [])

export const upsertJobMeta = (payload: {
  job_id: string
  repository: string
  snoozed_until?: string | null
  snooze_reason?: string
  archived?: boolean
  stale_days?: number
}): Promise<void> => api.put('/job-meta', payload).then(() => undefined)

export const deleteJobRuns = (jobId: string, repository: string): Promise<{ deleted_runs: number }> =>
  api
    .delete<{ deleted_runs: number }>('/job-runs', { params: { job_id: jobId, repository } })
    .then((r) => r.data)

export const fetchPolicies = (repository?: string): Promise<PolicyRule[]> =>
  api.get<PolicyRule[]>('/policies', { params: repository ? { repository } : {} }).then((r) => r.data ?? [])

export const upsertPolicy = (payload: {
  repository: string
  job_id?: string
  max_cost_per_hour: number
  enabled?: boolean
}): Promise<void> => api.put('/policies', payload).then(() => undefined)

export const deletePolicy = (repository: string, jobId = ''): Promise<void> =>
  api.delete('/policies', { params: { repository, job_id: jobId } }).then(() => undefined)

export const evaluatePolicy = (payload: {
  repository: string
  job_id: string
  detected_price_per_hour: number
}): Promise<PolicyEvaluation> => api.post<PolicyEvaluation>('/policies/evaluate', payload).then((r) => r.data)

export const fetchNotificationSettings = (): Promise<NotificationSettings> =>
  api.get<NotificationSettings>('/notifications/settings').then((r) => r.data)

export const upsertNotificationSettings = (payload: NotificationSettings): Promise<void> =>
  api.put('/notifications/settings', payload).then(() => undefined)

export const sendTestNotification = (): Promise<void> =>
  api.post('/notifications/test').then(() => undefined)

export const fetchDeliveryLogs = (ruleId?: string, limit = 50): Promise<DeliveryLog[]> =>
  api
    .get<DeliveryLog[]>('/notifications/deliveries', {
      params: { ...(ruleId ? { rule_id: ruleId } : {}), limit },
    })
    .then((r) => r.data ?? [])

export const fetchOwnership = (repository?: string): Promise<OwnershipEntry[]> =>
  api.get<OwnershipEntry[]>('/ownership', { params: repository ? { repository } : {} }).then((r) => r.data ?? [])

export const upsertOwnership = (payload: { repository: string; team_name: string; destination_ids: string[] }): Promise<void> =>
  api.put('/ownership', payload).then(() => undefined)

export const deleteOwnership = (repository: string, teamName: string): Promise<void> =>
  api.delete('/ownership', { params: { repository, team_name: teamName } }).then(() => undefined)

export interface UserSettings {
  otel_endpoint: string
  allowed_machine_ids: string[]
  allowed_series: string[]
  allowed_families: string[]
}

export const fetchUserSettings = (): Promise<UserSettings> =>
  api.get<UserSettings>('/user-settings').then((r) => r.data)

export const upsertUserSettings = (payload: UserSettings): Promise<void> =>
  api.put('/user-settings', payload).then(() => undefined)

// ── SSO API ─────────────────────────────────────────────────────────────────

export const fetchSSOProviders = (): Promise<SSOProvider[]> =>
  api.get<{ providers: SSOProvider[] }>('/sso/providers').then((r) => r.data?.providers ?? [])

export const fetchSSOMe = (): Promise<SSOUser> =>
  api.get<SSOUser>('/sso/me').then((r) => r.data)

export const ssoLogout = (): Promise<void> =>
  api.post('/sso/logout').then(() => undefined)

// ── RunRight Cloud signup API ────────────────────────────────────────────────

export interface CloudProviders {
  github: boolean
  google: boolean
}

export const fetchCloudProviders = (): Promise<CloudProviders> =>
  api.get<CloudProviders>('/cloud/providers').then((r) => r.data)

export interface CloudTenantStatus {
  status: 'provisioning' | 'active' | 'failed'
  slug: string
  claim_url?: string
  error?: string
}

export const fetchCloudTenantStatus = (tenantId: string): Promise<CloudTenantStatus> =>
  api.get<CloudTenantStatus>('/cloud/status', { params: { tenant: tenantId } }).then((r) => r.data)

// ── GitHub App status (used by LoginPage to show/hide "Sign in with GitHub") ─

export interface GitHubAppStatus {
  configured: boolean
  install_url?: string
  installation_count?: number
}

export const fetchGitHubStatus = (): Promise<GitHubAppStatus> =>
  api.get<GitHubAppStatus>('/github/status').then((r) => r.data)

// ── Workspace branding (instance name shown in sidebar/login) ───────────────

export interface WorkspaceSettings {
  name: string
  accent_color?: string
  logo_url?: string
}

export const fetchWorkspaceSettings = (): Promise<WorkspaceSettings> =>
  api.get<WorkspaceSettings>('/workspace').then((r) => r.data)

export const updateWorkspaceSettings = (settings: WorkspaceSettings): Promise<WorkspaceSettings> =>
  api.put<WorkspaceSettings>('/workspace', settings).then((r) => r.data)

// ── Free-plan usage caps + "request a quote" ────────────────────────────────

export interface UsageSummary {
  metered: boolean
  plan?: string
  plan_name?: string
  jobs_this_month?: number
  max_jobs_per_month?: number
  repos_connected?: number
  max_repos?: number
  jobs_at_cap?: boolean
  repos_at_cap?: boolean
}

export const fetchUsage = (): Promise<UsageSummary> =>
  api.get<UsageSummary>('/usage').then((r) => r.data)

export interface QuoteRequestInput {
  name: string
  email: string
  company?: string
  message?: string
  reason?: 'jobs_per_month' | 'repos' | 'proactive'
}

export const submitQuoteRequest = (input: QuoteRequestInput): Promise<{ id: string; status: string }> =>
  api.post('/quote-requests', input).then((r) => r.data)

// Admin SSO config management
export const fetchSSOConfigs = (): Promise<SSOConfig[]> =>
  api.get<{ configs: SSOConfig[] }>('/sso/configs').then((r) => r.data?.configs ?? [])

export const upsertSSOConfig = (payload: Partial<SSOConfig>): Promise<{ id: number }> =>
  api.put<{ id: number; status: string }>('/sso/configs', payload).then((r) => ({ id: r.data.id }))

export const deleteSSOConfig = (id: number): Promise<void> =>
  api.delete('/sso/configs', { data: { id } }).then(() => undefined)

export const testSSOConfig = (payload: Partial<SSOConfig>): Promise<{ valid: boolean; message?: string; error?: string }> =>
  api.post<{ valid: boolean; message?: string; error?: string }>('/sso/configs/test', payload).then((r) => r.data)

// ── User management API ─────────────────────────────────────────────────────

export const fetchUsers = (): Promise<SSOUser[]> =>
  api.get<{ users: SSOUser[] }>('/users').then((r) => r.data?.users ?? [])

export const updateUserRole = (email: string, role: string): Promise<void> =>
  api.put(`/users/${encodeURIComponent(email)}/role`, { role }).then(() => undefined)

// ── Role management API ──────────────────────────────────────────────────────

export const fetchRoles = (): Promise<{ roles: Role[]; available_permissions: string[] }> =>
  api.get<{ roles: Role[]; available_permissions: string[] }>('/roles').then((r) => r.data)

export const createRole = (payload: { name: string; description: string; permissions: string[] }): Promise<void> =>
  api.post('/roles', payload).then(() => undefined)

export const updateRole = (id: string, payload: { description?: string; permissions?: string[] }): Promise<void> =>
  api.put(`/roles/${id}`, payload).then(() => undefined)

export const deleteRole = (id: string): Promise<void> =>
  api.delete(`/roles/${id}`).then(() => undefined)

// ── AI Assistant API ────────────────────────────────────────────────────────

import type { Conversation, ChatMessage, ChatRequest, ChatResponse, AssistantStatus, QuickStats } from './types'

export const fetchAssistantStatus = (): Promise<AssistantStatus> =>
  api.get<AssistantStatus>('/assistant/status').then((r) => r.data)

export const fetchSuggestedQuestions = (): Promise<string[]> =>
  api.get<{ questions: string[] }>('/assistant/suggestions').then((r) => r.data?.questions ?? [])

export const fetchAssistantStats = (): Promise<QuickStats> =>
  api.get<QuickStats>('/assistant/stats').then((r) => r.data)

export const sendChatMessage = (payload: ChatRequest): Promise<ChatResponse> =>
  api.post<ChatResponse>('/assistant/chat', payload).then((r) => r.data)

export const fetchConversations = (): Promise<Conversation[]> =>
  api.get<Conversation[]>('/assistant/conversations').then((r) => r.data ?? [])

export const fetchConversation = (id: string): Promise<{ conversation: Conversation; messages: ChatMessage[] }> =>
  api.get<{ conversation: Conversation; messages: ChatMessage[] }>(`/assistant/conversations/${id}`).then((r) => r.data)

export const deleteConversation = (id: string): Promise<void> =>
  api.delete(`/assistant/conversations/${id}`).then(() => undefined)

export const deleteAllConversations = (): Promise<{ deleted: number }> =>
  api.delete<{ deleted: number }>('/assistant/conversations').then((r) => r.data)

// ── AI Assistant provider settings (Settings > General) ─────────────────────

export interface AISettings {
  provider: '' | 'openai' | 'anthropic' | 'ollama'
  model?: string
  base_url?: string
  api_key_set: boolean
}

export interface AISettingsInput {
  provider: 'openai' | 'anthropic' | 'ollama'
  api_key?: string // blank = leave the currently stored key untouched
  base_url?: string
  model?: string
}

export const fetchAISettings = (): Promise<AISettings> =>
  api.get<AISettings>('/ai-settings').then((r) => r.data)

export const updateAISettings = (settings: AISettingsInput): Promise<void> =>
  api.put('/ai-settings', settings).then(() => undefined)

export const deleteAISettings = (): Promise<void> =>
  api.delete('/ai-settings').then(() => undefined)

// ── Label Mappings & Auto-PR API ────────────────────────────────────────────

export interface LabelMapping {
  id: string
  team_id?: string
  repository: string
  label: string
  provider: string
  instance_type: string
  vcpus: number
  memory_gib: number
  cost_per_hour: number
  is_gpu: boolean
  gpu_type?: string
  gpu_count?: number
  gpu_memory_gib?: number
  created_at: string
  updated_at: string
}

export interface AutoPRSettings {
  team_id?: string
  enabled: boolean
  min_savings_percent: number
  require_consecutive_runs: number
  /** Min calendar days the qualifying runs must span. 0 = disabled. */
  min_data_days: number
  /** Max new recommendations created per background scan cycle. */
  max_recs_per_scan: number
  // Stored in DB but not yet surfaced in the UI (kept for future use):
  min_monthly_savings?: number
  gpu_prs_enabled?: boolean
  gpu_min_savings_percent?: number
  exclude_repositories?: string[]
  exclude_job_patterns?: string[]
  /** Write-only on PUT — never returned by GET. Use github_token_set / hint. */
  github_token?: string
  github_token_set?: boolean
  github_token_hint?: string // e.g. "…a1b2"
}

export interface PRRecommendation {
  id: string
  team_id?: string
  repository: string
  job_id: string
  workflow_file?: string
  current_label: string
  current_vcpus: number
  current_memory_gib: number
  current_cost_per_hour: number
  recommended_label: string
  recommended_vcpus: number
  recommended_memory_gib: number
  recommended_cost_per_hour: number
  p95_cpu_percent: number
  p95_mem_percent: number
  run_count: number
  consecutive_underutilized: number
  is_gpu_job: boolean
  current_gpu_type?: string
  recommended_gpu_type?: string
  p95_gpu_util_percent?: number
  p95_gpu_mem_percent?: number
  savings_percent: number
  monthly_savings_usd: number
  status: string
  pr_url?: string
  pr_number?: number
  dismissed_reason?: string
  created_at: string
  updated_at: string
}

export interface GPUTier {
  type: string
  name: string
  memory_gib: number
  cost_per_hour: number
  provider: string
}

export interface PRHistory {
  id: string
  recommendation_id?: string
  team_id?: string
  repository: string
  job_id: string
  pr_number: number
  pr_url: string
  old_label: string
  new_label: string
  savings_percent: number
  monthly_savings_usd: number
  is_gpu_job: boolean
  status: string
  created_at: string
  merged_at?: string
  closed_at?: string
}

// Label Mappings
export const fetchLabelMappings = (gpuOnly = false, repository?: string): Promise<LabelMapping[]> =>
  api.get<LabelMapping[]>('/labels', { 
    params: { 
      ...(gpuOnly ? { gpu: 'true' } : {}),
      ...(repository ? { repository } : {})
    } 
  }).then((r) => r.data ?? [])

export const upsertLabelMapping = (mapping: Partial<LabelMapping>): Promise<{ id: string }> =>
  api.put<{ id: string; status: string }>('/labels', mapping).then((r) => r.data)

export const deleteLabelMapping = (id: string): Promise<void> =>
  api.delete(`/labels/${id}`).then(() => undefined)

// Auto-PR Settings
export const fetchAutoPRSettings = (): Promise<AutoPRSettings> =>
  api.get<AutoPRSettings>('/auto-pr/settings').then((r) => r.data)

export const upsertAutoPRSettings = (settings: Partial<AutoPRSettings>): Promise<void> =>
  api.put('/auto-pr/settings', settings).then(() => undefined)

export const triggerAutoPRScan = (): Promise<void> =>
  api.post('/auto-pr/scan').then(() => undefined)

// PR Recommendations
export const fetchPRRecommendations = (status = 'pending', gpuOnly = false): Promise<PRRecommendation[]> =>
  api.get<PRRecommendation[]>('/auto-pr/recommendations', { 
    params: { status, ...(gpuOnly ? { gpu: 'true' } : {}) } 
  }).then((r) => r.data ?? [])

export const createPRRecommendation = (rec: Partial<PRRecommendation>): Promise<{ id: string }> =>
  api.post<{ id: string; status: string }>('/auto-pr/recommendations', rec).then((r) => r.data)

export const approvePRRecommendation = (id: string): Promise<{ status: string; pr_url?: string; pr_number?: number }> =>
  api.post<{ status: string; pr_url?: string; pr_number?: number }>(`/auto-pr/recommendations/${id}/approve`).then((r) => r.data)

export const dismissPRRecommendation = (id: string, reason: string): Promise<void> =>
  api.post(`/auto-pr/recommendations/${id}/dismiss`, { reason }).then(() => undefined)

// PR History
export const fetchPRHistory = (status?: string, gpuOnly = false): Promise<PRHistory[]> =>
  api.get<PRHistory[]>('/auto-pr/history', { 
    params: { 
      ...(status ? { status } : {}),
      ...(gpuOnly ? { gpu: 'true' } : {})
    } 
  }).then((r) => r.data ?? [])

// GPU Analysis
export const fetchGPUTiers = (): Promise<GPUTier[]> =>
  api.get<GPUTier[]>('/gpu/tiers').then((r) => r.data ?? [])

export const getGPURecommendation = (params: {
  current_gpu: string
  p95_util_percent: number
  p95_mem_percent: number
  peak_memory_gib: number
  avg_duration_sec: number
  runs_per_month: number
}): Promise<{
  current_gpu: string
  recommended_gpu: string
  reason: string
  savings_percent: number
  monthly_savings_usd: number
  action: string
}> => api.post('/gpu/recommendation', params).then((r) => r.data)
