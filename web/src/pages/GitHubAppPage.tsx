import { useEffect, useState, useCallback } from 'react'

// Inline SVG icons (no external icon library dependency)
const GitBranchIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="6" y1="3" x2="6" y2="15" />
    <circle cx="18" cy="6" r="3" />
    <circle cx="6" cy="18" r="3" />
    <path d="M18 9a9 9 0 0 1-9 9" />
  </svg>
)

const GitPullRequestIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <circle cx="18" cy="18" r="3" />
    <circle cx="6" cy="6" r="3" />
    <path d="M13 6h3a2 2 0 0 1 2 2v7" />
    <line x1="6" y1="9" x2="6" y2="21" />
  </svg>
)

const CheckCircleIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
    <polyline points="22 4 12 14.01 9 11.01" />
  </svg>
)

const AlertCircleIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <circle cx="12" cy="12" r="10" />
    <line x1="12" y1="8" x2="12" y2="12" />
    <line x1="12" y1="16" x2="12.01" y2="16" />
  </svg>
)

const ExternalLinkIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
    <polyline points="15 3 21 3 21 9" />
    <line x1="10" y1="14" x2="21" y2="3" />
  </svg>
)

const RefreshCwIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="23 4 23 10 17 10" />
    <polyline points="1 20 1 14 7 14" />
    <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
  </svg>
)

const ZapIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
  </svg>
)

const BuildingIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <rect x="4" y="2" width="16" height="20" rx="2" ry="2" />
    <line x1="9" y1="22" x2="9" y2="2" />
    <line x1="14" y1="2" x2="14" y2="22" />
    <line x1="4" y1="12" x2="9" y2="12" />
    <line x1="14" y1="12" x2="20" y2="12" />
  </svg>
)

const UsersIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
    <path d="M16 3.13a4 4 0 0 1 0 7.75" />
  </svg>
)

const ClockIcon = ({ className }: { className?: string }) => (
  <svg className={className} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <circle cx="12" cy="12" r="10" />
    <polyline points="12 6 12 12 16 14" />
  </svg>
)

interface Installation {
  installation_id: number
  account_type: string
  account_login: string
  account_id: number
  target_type: string
  suspended_at?: string
  created_at: string
  repo_count: number
}

interface WorkflowRun {
  run_id: number
  repo_name: string
  workflow_name: string
  head_branch: string
  head_sha: string
  event: string
  status: string
  conclusion: string
  pr_number?: number
  artifact_downloaded: boolean
  metrics_processed: boolean
  comment_posted: boolean
  run_started_at?: string
  run_completed_at?: string
  created_at?: string
}

interface AppStatus {
  configured: boolean
  app_id?: number
  app_slug?: string
  install_url?: string
  installation_count: number
}

interface Repo {
  repo_id: number
  repo_name: string
  private: boolean
  created_at: string
}

const API_BASE = import.meta.env.VITE_API_URL || ''
// Raw fetch() never times out on its own — a cold-starting Machine or a
// stuck connection would otherwise spin the loading state forever with no
// feedback. Every call below aborts and surfaces a real error after 12s.
const FETCH_TIMEOUT_MS = 12_000

async function fetchWithTimeout(url: string, init?: RequestInit): Promise<Response> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS)
  try {
    return await fetch(url, { ...init, signal: controller.signal })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new Error('Request timed out — the server took too long to respond.')
    }
    throw err
  } finally {
    clearTimeout(timer)
  }
}

async function fetchAppStatus(): Promise<AppStatus> {
  const res = await fetchWithTimeout(`${API_BASE}/api/v1/github/status`)
  if (!res.ok) throw new Error('Failed to fetch app status')
  return res.json()
}

async function fetchInstallations(): Promise<Installation[]> {
  const res = await fetchWithTimeout(`${API_BASE}/api/v1/github/installations`, {
    credentials: 'include',
  })
  if (!res.ok) throw new Error('Failed to fetch installations')
  return res.json()
}

async function fetchInstallationRepos(installationId: number): Promise<Repo[]> {
  const res = await fetchWithTimeout(`${API_BASE}/api/v1/github/installations/${installationId}/repos`, {
    credentials: 'include',
  })
  if (!res.ok) throw new Error('Failed to fetch repos')
  return res.json()
}

async function fetchWorkflowRuns(repo?: string): Promise<WorkflowRun[]> {
  const url = repo 
    ? `${API_BASE}/api/v1/github/workflow-runs?repository=${encodeURIComponent(repo)}`
    : `${API_BASE}/api/v1/github/workflow-runs`
  const res = await fetchWithTimeout(url, { credentials: 'include' })
  if (!res.ok) throw new Error('Failed to fetch workflow runs')
  return res.json()
}

async function injectWorkflow(repository: string): Promise<{ pr_url: string; pr_number: number }> {
  const res = await fetchWithTimeout(`${API_BASE}/api/v1/github/inject-workflow`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ repository }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to inject workflow')
  }
  return res.json()
}

type TabId = 'overview' | 'installations' | 'runs' | 'setup'

export default function GitHubAppPage() {
  const [activeTab, setActiveTab] = useState<TabId>('overview')
  const [appStatus, setAppStatus] = useState<AppStatus | null>(null)
  const [installations, setInstallations] = useState<Installation[]>([])
  const [selectedInstallation, setSelectedInstallation] = useState<Installation | null>(null)
  const [repos, setRepos] = useState<Repo[]>([])
  const [runs, setRuns] = useState<WorkflowRun[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [injecting, setInjecting] = useState<string | null>(null)

  const loadData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [status, installs, workflowRuns] = await Promise.all([
        fetchAppStatus(),
        fetchInstallations().catch(() => []),
        fetchWorkflowRuns().catch(() => []),
      ])
      setAppStatus(status)
      setInstallations(installs || [])
      setRuns(workflowRuns || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load GitHub App data')
      console.error(err)
    }
    setLoading(false)
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  useEffect(() => {
    if (note) {
      const t = setTimeout(() => setNote(''), 5000)
      return () => clearTimeout(t)
    }
  }, [note])

  const handleSelectInstallation = async (inst: Installation) => {
    setSelectedInstallation(inst)
    try {
      const repoList = await fetchInstallationRepos(inst.installation_id)
      setRepos(repoList || [])
    } catch (err) {
      console.error(err)
      setRepos([])
    }
  }

  const handleInjectWorkflow = async (repoName: string) => {
    setInjecting(repoName)
    try {
      const result = await injectWorkflow(repoName)
      setNote(`✅ PR created: ${result.pr_url}`)
    } catch (err: any) {
      setNote(`❌ ${err.message}`)
    }
    setInjecting(null)
  }

  const tabs: { id: TabId; label: string; icon: React.ReactNode }[] = [
    { id: 'overview', label: 'Overview', icon: <ZapIcon className="w-4 h-4" /> },
    { id: 'installations', label: 'Installations', icon: <BuildingIcon className="w-4 h-4" /> },
    { id: 'runs', label: 'Workflow Runs', icon: <GitBranchIcon className="w-4 h-4" /> },
    { id: 'setup', label: 'Setup Guide', icon: <UsersIcon className="w-4 h-4" /> },
  ]

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center h-64 gap-3">
        <RefreshCwIcon className="w-8 h-8 animate-spin text-blue-500" />
        <p className="text-sm text-gray-500 dark:text-gray-400">Loading GitHub App status&hellip;</p>
      </div>
    )
  }

  return (
    <div className="p-6 max-w-7xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white flex items-center gap-2">
            <GitBranchIcon className="w-7 h-7" />
            GitHub App
          </h1>
          <p className="text-gray-500 dark:text-gray-400 mt-1">
            Manage GitHub App installations and monitor workflow runs
          </p>
        </div>
        <button
          onClick={loadData}
          className="flex items-center gap-2 px-4 py-2 bg-gray-100 dark:bg-gray-700 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-600 transition"
        >
          <RefreshCwIcon className="w-4 h-4" />
          Refresh
        </button>
      </div>

      {error && (
        <div className="mb-4 p-4 bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 rounded-lg flex items-center justify-between gap-4">
          <span>{error}</span>
          <button
            onClick={loadData}
            className="shrink-0 px-3 py-1.5 text-sm font-medium bg-red-600 text-white rounded-md hover:bg-red-700 transition-colors"
          >
            Retry
          </button>
        </div>
      )}

      {note && (
        <div className="mb-4 p-4 bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 rounded-lg">
          {note}
        </div>
      )}

      {/* Tabs */}
      <div className="border-b border-gray-200 dark:border-gray-700 mb-6">
        <nav className="flex gap-4">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={`flex items-center gap-2 px-4 py-3 border-b-2 transition ${
                activeTab === tab.id
                  ? 'border-blue-500 text-blue-600 dark:text-blue-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 dark:hover:text-gray-300'
              }`}
            >
              {tab.icon}
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* Overview Tab */}
      {activeTab === 'overview' && (
        <div className="space-y-6">
          {/* Status Card */}
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6">
            <h2 className="text-lg font-semibold mb-4">App Status</h2>
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
              <div className="p-4 bg-gray-50 dark:bg-gray-700 rounded-lg">
                <div className="text-sm text-gray-500 dark:text-gray-400">Status</div>
                <div className="flex items-center gap-2 mt-1">
                  {appStatus?.configured ? (
                    <>
                      <CheckCircleIcon className="w-5 h-5 text-green-500" />
                      <span className="font-medium text-green-600 dark:text-green-400">Configured</span>
                    </>
                  ) : (
                    <>
                      <AlertCircleIcon className="w-5 h-5 text-yellow-500" />
                      <span className="font-medium text-yellow-600 dark:text-yellow-400">Not Configured</span>
                    </>
                  )}
                </div>
              </div>
              <div className="p-4 bg-gray-50 dark:bg-gray-700 rounded-lg">
                <div className="text-sm text-gray-500 dark:text-gray-400">Installations</div>
                <div className="text-2xl font-bold mt-1">{appStatus?.installation_count || 0}</div>
              </div>
              <div className="p-4 bg-gray-50 dark:bg-gray-700 rounded-lg">
                <div className="text-sm text-gray-500 dark:text-gray-400">Total Repos</div>
                <div className="text-2xl font-bold mt-1">
                  {installations.reduce((sum, i) => sum + i.repo_count, 0)}
                </div>
              </div>
              <div className="p-4 bg-gray-50 dark:bg-gray-700 rounded-lg">
                <div className="text-sm text-gray-500 dark:text-gray-400">Recent Runs</div>
                <div className="text-2xl font-bold mt-1">{runs.length}</div>
              </div>
            </div>

            {appStatus?.install_url && (
              <a
                href={appStatus.install_url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 mt-4 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition"
              >
                Install GitHub App
                <ExternalLinkIcon className="w-4 h-4" />
              </a>
            )}
          </div>

          {/* Recent Activity */}
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6">
            <h2 className="text-lg font-semibold mb-4">Recent Workflow Runs</h2>
            {runs.length === 0 ? (
              <p className="text-gray-500 dark:text-gray-400">
                No workflow runs yet. Install the app on a repository to start monitoring.
              </p>
            ) : (
              <div className="space-y-3">
                {runs.slice(0, 5).map((run) => (
                  <div
                    key={run.run_id}
                    className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg"
                  >
                    <div className="flex items-center gap-3">
                      {run.conclusion === 'success' ? (
                        <CheckCircleIcon className="w-5 h-5 text-green-500" />
                      ) : run.conclusion === 'failure' ? (
                        <AlertCircleIcon className="w-5 h-5 text-red-500" />
                      ) : (
                        <ClockIcon className="w-5 h-5 text-yellow-500" />
                      )}
                      <div>
                        <div className="font-medium">{run.repo_name}</div>
                        <div className="text-sm text-gray-500 dark:text-gray-400">
                          {run.workflow_name} • {run.head_branch}
                        </div>
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      {run.metrics_processed && (
                        <span className="px-2 py-1 bg-green-100 dark:bg-green-900/20 text-green-600 dark:text-green-400 text-xs rounded">
                          Metrics
                        </span>
                      )}
                      {run.comment_posted && (
                        <span className="px-2 py-1 bg-blue-100 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 text-xs rounded">
                          PR Comment
                        </span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Installations Tab */}
      {activeTab === 'installations' && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Installations List */}
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6">
            <h2 className="text-lg font-semibold mb-4">Installations</h2>
            {installations.length === 0 ? (
              <div className="text-center py-8">
                <BuildingIcon className="w-12 h-12 mx-auto text-gray-400 mb-4" />
                <p className="text-gray-500 dark:text-gray-400 mb-4">No installations yet</p>
                {appStatus?.install_url && (
                  <a
                    href={appStatus.install_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition"
                  >
                    Install on GitHub
                    <ExternalLinkIcon className="w-4 h-4" />
                  </a>
                )}
              </div>
            ) : (
              <div className="space-y-3">
                {installations.map((inst) => (
                  <button
                    key={inst.installation_id}
                    onClick={() => handleSelectInstallation(inst)}
                    className={`w-full text-left p-4 rounded-lg border transition ${
                      selectedInstallation?.installation_id === inst.installation_id
                        ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20'
                        : 'border-gray-200 dark:border-gray-600 hover:border-gray-300 dark:hover:border-gray-500'
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-3">
                        {inst.account_type === 'Organization' ? (
                          <BuildingIcon className="w-5 h-5 text-gray-500" />
                        ) : (
                          <UsersIcon className="w-5 h-5 text-gray-500" />
                        )}
                        <div>
                          <div className="font-medium">{inst.account_login}</div>
                          <div className="text-sm text-gray-500 dark:text-gray-400">
                            {inst.repo_count} repos • {inst.target_type === 'all' ? 'All repos' : 'Selected repos'}
                          </div>
                        </div>
                      </div>
                      {inst.suspended_at && (
                        <span className="px-2 py-1 bg-yellow-100 dark:bg-yellow-900/20 text-yellow-600 dark:text-yellow-400 text-xs rounded">
                          Suspended
                        </span>
                      )}
                    </div>
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* Repos for Selected Installation */}
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6">
            <h2 className="text-lg font-semibold mb-4">
              {selectedInstallation ? `Repos in ${selectedInstallation.account_login}` : 'Select an Installation'}
            </h2>
            {!selectedInstallation ? (
              <p className="text-gray-500 dark:text-gray-400">
                Select an installation to view its repositories
              </p>
            ) : repos.length === 0 ? (
              <p className="text-gray-500 dark:text-gray-400">No repositories found</p>
            ) : (
              <div className="space-y-2 max-h-96 overflow-y-auto">
                {repos.map((repo) => (
                  <div
                    key={repo.repo_id}
                    className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg"
                  >
                    <div className="flex items-center gap-2">
                      <GitBranchIcon className="w-4 h-4 text-gray-500" />
                      <span className="font-medium">{repo.repo_name}</span>
                      {repo.private && (
                        <span className="px-1.5 py-0.5 bg-gray-200 dark:bg-gray-600 text-xs rounded">
                          Private
                        </span>
                      )}
                    </div>
                    <button
                      onClick={() => handleInjectWorkflow(repo.repo_name)}
                      disabled={injecting === repo.repo_name}
                      className="flex items-center gap-1 px-3 py-1.5 bg-blue-600 text-white text-sm rounded hover:bg-blue-700 disabled:opacity-50 transition"
                    >
                      {injecting === repo.repo_name ? (
                        <RefreshCwIcon className="w-3 h-3 animate-spin" />
                      ) : (
                        <GitPullRequestIcon className="w-3 h-3" />
                      )}
                      Add RunRight
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Workflow Runs Tab */}
      {activeTab === 'runs' && (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6">
          <h2 className="text-lg font-semibold mb-4">Workflow Runs</h2>
          {runs.length === 0 ? (
            <p className="text-gray-500 dark:text-gray-400">No workflow runs recorded yet</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="text-left border-b border-gray-200 dark:border-gray-700">
                    <th className="pb-3 font-medium">Repository</th>
                    <th className="pb-3 font-medium">Workflow</th>
                    <th className="pb-3 font-medium">Branch</th>
                    <th className="pb-3 font-medium">Status</th>
                    <th className="pb-3 font-medium">Metrics</th>
                    <th className="pb-3 font-medium">PR Comment</th>
                  </tr>
                </thead>
                <tbody>
                  {runs.map((run) => (
                    <tr key={run.run_id} className="border-b border-gray-100 dark:border-gray-700">
                      <td className="py-3">{run.repo_name}</td>
                      <td className="py-3">{run.workflow_name}</td>
                      <td className="py-3">
                        <code className="px-2 py-1 bg-gray-100 dark:bg-gray-700 rounded text-sm">
                          {run.head_branch}
                        </code>
                      </td>
                      <td className="py-3">
                        <span
                          className={`px-2 py-1 rounded text-xs ${
                            run.conclusion === 'success'
                              ? 'bg-green-100 dark:bg-green-900/20 text-green-600 dark:text-green-400'
                              : run.conclusion === 'failure'
                              ? 'bg-red-100 dark:bg-red-900/20 text-red-600 dark:text-red-400'
                              : 'bg-yellow-100 dark:bg-yellow-900/20 text-yellow-600 dark:text-yellow-400'
                          }`}
                        >
                          {run.conclusion || run.status}
                        </span>
                      </td>
                      <td className="py-3">
                        {run.metrics_processed ? (
                          <CheckCircleIcon className="w-5 h-5 text-green-500" />
                        ) : (
                          <span className="text-gray-400">—</span>
                        )}
                      </td>
                      <td className="py-3">
                        {run.comment_posted ? (
                          <CheckCircleIcon className="w-5 h-5 text-green-500" />
                        ) : (
                          <span className="text-gray-400">—</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* Setup Guide Tab */}
      {activeTab === 'setup' && (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6">
          <h2 className="text-lg font-semibold mb-4">Setup Guide</h2>
          <div className="prose dark:prose-invert max-w-none">
            <h3>1. Install the GitHub App</h3>
            <p>
              Click the button below to install RunRight on your GitHub organization or personal account.
              You can choose to install on all repositories or select specific ones.
            </p>
            {appStatus?.install_url && (
              <a
                href={appStatus.install_url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition no-underline"
              >
                Install GitHub App
                <ExternalLinkIcon className="w-4 h-4" />
              </a>
            )}

            <h3 className="mt-6">2. Add RunRight to Your Workflows</h3>
            <p>
              After installation, go to the <strong>Installations</strong> tab, select your organization,
              and click "Add RunRight" next to any repository. This creates a PR that adds the RunRight
              action to your existing workflows.
            </p>

            <h3 className="mt-6">3. Monitor Your CI</h3>
            <p>
              Once merged, RunRight will automatically:
            </p>
            <ul>
              <li>Monitor CPU and memory usage during CI runs</li>
              <li>Post cost optimization recommendations on PRs</li>
              <li>Track metrics in your RunRight dashboard</li>
            </ul>

            <h3 className="mt-6">Environment Variables</h3>
            <p>To configure the GitHub App backend, set these environment variables:</p>
            <pre className="bg-gray-100 dark:bg-gray-900 p-4 rounded-lg overflow-x-auto">
{`GITHUB_APP_ID=your_app_id
GITHUB_APP_SLUG=runright-ci
GITHUB_APP_CLIENT_ID=your_client_id
GITHUB_APP_CLIENT_SECRET=your_client_secret
GITHUB_APP_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----..."
GITHUB_APP_WEBHOOK_SECRET=your_webhook_secret`}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}
