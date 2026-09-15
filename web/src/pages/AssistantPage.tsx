import React, { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  fetchAssistantStatus,
  fetchSuggestedQuestions,
  fetchAssistantStats,
  sendChatMessage,
  fetchConversations,
  fetchConversation,
  deleteConversation,
  deleteAllConversations,
} from '../api'
import type { ChatMessage, Conversation, QuickStats, DataSource, AssistantStatus } from '../types'
import { ConfirmModal } from '../components/ConfirmModal'
import { usePageContext } from '../hooks/usePageContext'

// ─── Icons ──────────────────────────────────────────────────────────────────
const SendIcon = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="22" y1="2" x2="11" y2="13" /><polygon points="22 2 15 22 11 13 2 9 22 2" />
  </svg>
)

const PlusIcon = () => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
    <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
  </svg>
)

const TrashIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
  </svg>
)

const SparkleIcon = () => (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
    <path d="M12 2L13.09 8.26L19 9L13.09 9.74L12 16L10.91 9.74L5 9L10.91 8.26L12 2Z" />
    <path d="M5 16L5.54 18.2L8 18.5L5.54 18.8L5 21L4.46 18.8L2 18.5L4.46 18.2L5 16Z" opacity="0.7" />
    <path d="M19 14L19.36 15.4L21 15.6L19.36 15.8L19 17.2L18.64 15.8L17 15.6L18.64 15.4L19 14Z" opacity="0.7" />
  </svg>
)

const BotIcon = () => (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <rect x="3" y="11" width="18" height="10" rx="2" />
    <circle cx="12" cy="5" r="2" />
    <path d="M12 7v4" />
    <line x1="8" y1="16" x2="8" y2="16" />
    <line x1="16" y1="16" x2="16" y2="16" />
  </svg>
)

const ChevronIcon = ({ direction = 'left' }: { direction?: 'left' | 'right' }) => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
    style={{ transform: direction === 'right' ? 'rotate(180deg)' : undefined }}>
    <polyline points="15 18 9 12 15 6" />
  </svg>
)

const EditIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
  </svg>
)

const RetryIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="23 4 23 10 17 10" />
    <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
  </svg>
)

// ─── Helpers ────────────────────────────────────────────────────────────────
const fmt = (n: number, decimals = 2) => n.toLocaleString('en-US', { maximumFractionDigits: decimals })
const fmtCurrency = (n: number) => `$${fmt(n, 2)}`
const fmtPercent = (n: number) => `${fmt(n, 1)}%`

// Simple markdown to JSX
const renderMarkdown = (content: string) => {
  const lines = content.split('\n')
  const elements: React.ReactElement[] = []
  let inCodeBlock = false
  let codeLines: string[] = []
  let codeLang = ''

  lines.forEach((line, i) => {
    // Code blocks
    if (line.startsWith('```')) {
      if (!inCodeBlock) {
        inCodeBlock = true
        codeLang = line.slice(3).trim()
        codeLines = []
      } else {
        elements.push(
          <pre key={i} className="my-2 p-3 rounded-lg bg-[var(--ink)]/10 overflow-x-auto text-sm" data-lang={codeLang}>
            <code className="font-mono">{codeLines.join('\n')}</code>
          </pre>
        )
        inCodeBlock = false
      }
      return
    }

    if (inCodeBlock) {
      codeLines.push(line)
      return
    }

    // Headers
    if (line.startsWith('### ')) {
      elements.push(<h4 key={i} className="font-semibold text-[var(--text)] mt-3 mb-1">{line.slice(4)}</h4>)
      return
    }
    if (line.startsWith('## ')) {
      elements.push(<h3 key={i} className="font-semibold text-[var(--text)] text-lg mt-4 mb-2">{line.slice(3)}</h3>)
      return
    }
    if (line.startsWith('# ')) {
      elements.push(<h2 key={i} className="font-bold text-[var(--text)] text-xl mt-4 mb-2">{line.slice(2)}</h2>)
      return
    }

    // Lists
    if (line.match(/^[-*]\s/)) {
      elements.push(<li key={i} className="ml-4 list-disc">{formatInline(line.slice(2))}</li>)
      return
    }
    if (line.match(/^\d+\.\s/)) {
      const text = line.replace(/^\d+\.\s/, '')
      elements.push(<li key={i} className="ml-4 list-decimal">{formatInline(text)}</li>)
      return
    }

    // Regular paragraph
    if (line.trim()) {
      elements.push(<p key={i} className="mb-2 last:mb-0">{formatInline(line)}</p>)
    }
  })

  return elements
}

const formatInline = (text: string) => {
  // Bold
  text = text.replace(/\*\*(.+?)\*\*/g, '<strong class="font-semibold text-[var(--text)]">$1</strong>')
  // Inline code
  text = text.replace(/`([^`]+)`/g, '<code class="px-1.5 py-0.5 rounded bg-[var(--ink)]/10 font-mono text-sm">$1</code>')
  // Links - style internal links specially
  text = text.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, linkText, href) => {
    const isInternal = href.startsWith('/app/')
    const classes = isInternal 
      ? 'text-[var(--gold)] hover:underline cursor-pointer font-medium'
      : 'text-[var(--gold)] hover:underline'
    return `<a href="${href}" class="${classes}" data-internal="${isInternal}">${linkText}</a>`
  })
  // Return as HTML
  return <span dangerouslySetInnerHTML={{ __html: text }} />
}

// ─── Main Component ─────────────────────────────────────────────────────────
export default function AssistantPage() {
  const [status, setStatus] = useState<AssistantStatus | null>(null)
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [currentConvId, setCurrentConvId] = useState<string | null>(null)
  const [currentConvSummary, setCurrentConvSummary] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [stats, setStats] = useState<QuickStats | null>(null)
  const [dataSources, setDataSources] = useState<DataSource[]>([])
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editText, setEditText] = useState('')
  const [summaryExpanded, setSummaryExpanded] = useState(false)
  const [confirmModal, setConfirmModal] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)

  const pageContext = usePageContext()
  const navigate = useNavigate()
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // Handle clicks on internal links in assistant messages
  const handleMessageClick = useCallback((e: React.MouseEvent) => {
    const target = e.target as HTMLElement
    if (target.tagName === 'A') {
      const href = target.getAttribute('href')
      if (href?.startsWith('/app/')) {
        e.preventDefault()
        navigate(href)
      }
    }
  }, [navigate])

  // Check status on mount
  useEffect(() => {
    fetchAssistantStatus().then(setStatus).catch(() => setStatus({ configured: false }))
  }, [])

  // Load data when status resolves
  useEffect(() => {
    if (status === null) return
    fetchConversations().then(setConversations).catch(console.error)
    if (status?.configured) {
      fetchSuggestedQuestions().then(setSuggestions).catch(console.error)
      fetchAssistantStats().then(setStats).catch(console.error)
    }
  }, [status?.configured])

  // Auto-scroll
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Auto-resize textarea
  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value)
    e.target.style.height = 'auto'
    e.target.style.height = Math.min(e.target.scrollHeight, 200) + 'px'
  }

  const loadConversation = useCallback(async (id: string) => {
    try {
      const { messages: msgs, conversation } = await fetchConversation(id)
      setCurrentConvId(id)
      setMessages(msgs ?? [])
      setCurrentConvSummary(conversation?.summary ?? null)
      setSummaryExpanded(false)
      setDataSources([])
    } catch (e) {
      console.error('Failed to load conversation', e)
    }
  }, [])

  const newConversation = useCallback(() => {
    setCurrentConvId(null)
    setCurrentConvSummary(null)
    setSummaryExpanded(false)
    setMessages([])
    setDataSources([])
    inputRef.current?.focus()
  }, [])

  const handleDeleteConversation = useCallback((id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    setConfirmModal({
      title: 'Delete conversation',
      message: 'This conversation will be permanently deleted.',
      onConfirm: async () => {
        setConfirmModal(null)
        try {
          await deleteConversation(id)
          setConversations(prev => prev.filter(c => c.id !== id))
          if (currentConvId === id) newConversation()
        } catch (err) {
          console.error('Failed to delete conversation', err)
        }
      },
    })
  }, [currentConvId, newConversation])

  const handleClearAllHistory = useCallback(() => {
    setConfirmModal({
      title: 'Clear all history',
      message: 'All conversations will be permanently deleted. This cannot be undone.',
      onConfirm: async () => {
        setConfirmModal(null)
        try {
          await deleteAllConversations()
          setConversations([])
          newConversation()
        } catch (err) {
          console.error('Failed to clear history', err)
        }
      },
    })
  }, [newConversation])

  const handleSend = useCallback(async (messageText?: string) => {
    const text = messageText ?? input.trim()
    if (!text || loading) return

    setInput('')
    if (inputRef.current) inputRef.current.style.height = 'auto'
    setLoading(true)

    const userMsg: ChatMessage = {
      id: `temp-${Date.now()}`,
      conversation_id: currentConvId ?? '',
      role: 'user',
      content: text,
      created_at: new Date().toISOString(),
    }
    setMessages(prev => [...prev, userMsg])

    try {
      const response = await sendChatMessage({
        conversation_id: currentConvId ?? undefined,
        message: text,
        page_context: pageContext,
      })

      if (!currentConvId) {
        setCurrentConvId(response.conversation_id)
        fetchConversations().then(setConversations)
      }

      setMessages(prev => {
        const updated = prev.map(m =>
          m.id === userMsg.id ? { ...m, id: `user-${Date.now()}`, conversation_id: response.conversation_id } : m
        )
        return [...updated, response.message]
      })

      if (response.data_sources) setDataSources(response.data_sources)
    } catch (e: unknown) {
      const errorMsg = e instanceof Error ? e.message : 'Failed to send message'
      setMessages(prev => [
        ...prev,
        {
          id: `error-${Date.now()}`,
          conversation_id: currentConvId ?? '',
          role: 'assistant',
          content: `I encountered an error: ${errorMsg}\n\nPlease check that the AI service is running and try again.`,
          created_at: new Date().toISOString(),
        },
      ])
    } finally {
      setLoading(false)
    }
  }, [input, loading, currentConvId, pageContext])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  // Edit a user message - removes this message and all after, then sends edited version
  const handleEdit = useCallback((msgId: string) => {
    const msg = messages.find(m => m.id === msgId)
    if (msg) {
      setEditingId(msgId)
      setEditText(msg.content)
    }
  }, [messages])

  const handleCancelEdit = useCallback(() => {
    setEditingId(null)
    setEditText('')
  }, [])

  const handleSaveEdit = useCallback(async () => {
    if (!editingId || !editText.trim() || loading) return
    
    // Find index of the message being edited
    const idx = messages.findIndex(m => m.id === editingId)
    if (idx === -1) return
    
    // Remove this message and all messages after it
    setMessages(prev => prev.slice(0, idx))
    setEditingId(null)
    setEditText('')
    
    // Send the edited message
    await handleSend(editText.trim())
  }, [editingId, editText, loading, messages, handleSend])

  // Retry last user message
  const handleRetry = useCallback(async (msgId: string) => {
    const msg = messages.find(m => m.id === msgId)
    if (!msg || loading) return
    
    // Find index of this message
    const idx = messages.findIndex(m => m.id === msgId)
    if (idx === -1) return
    
    // Remove this message and all after
    setMessages(prev => prev.slice(0, idx))
    
    // Resend
    await handleSend(msg.content)
  }, [messages, loading, handleSend])

  // Not configured
  if (status && !status.configured) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="max-w-md text-center px-6">
          <div className="w-20 h-20 mx-auto mb-8 rounded-2xl bg-gradient-to-br from-[var(--gold)]/20 to-[var(--gold)]/5 flex items-center justify-center">
            <svg width="36" height="36" viewBox="0 0 24 24" fill="none" className="text-[var(--gold)]">
              <path d="M12 3v2M12 19v2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M3 12h2M19 12h2M5.6 18.4l1.4-1.4M17 7l1.4-1.4" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
              <circle cx="12" cy="12" r="4" stroke="currentColor" strokeWidth="2"/>
            </svg>
          </div>
          <h2 className="text-2xl font-deco tracking-wide text-[var(--text)] mb-3">AI Assistant</h2>
          <p className="text-[var(--text-mid)] mb-8 leading-relaxed">
            Connect an AI model to get intelligent insights about your CI/CD costs and optimization opportunities.
          </p>
          
          <div className="space-y-3">
            <div className="flex items-center gap-3 p-4 rounded-xl bg-[var(--ink)]/[0.03] border border-[var(--border)]">
              <div className="w-10 h-10 rounded-lg bg-orange-500/10 flex items-center justify-center flex-shrink-0">
                <span className="text-lg">🦙</span>
              </div>
              <div className="text-left">
                <p className="font-medium text-[var(--text)]">Ollama</p>
                <p className="text-xs text-[var(--text-mid)]">Self-hosted, private, free</p>
              </div>
            </div>
            
            <div className="flex items-center gap-3 p-4 rounded-xl bg-[var(--ink)]/[0.03] border border-[var(--border)]">
              <div className="w-10 h-10 rounded-lg bg-emerald-500/10 flex items-center justify-center flex-shrink-0">
                <span className="text-lg">✨</span>
              </div>
              <div className="text-left">
                <p className="font-medium text-[var(--text)]">OpenAI / Anthropic</p>
                <p className="text-xs text-[var(--text-mid)]">Cloud APIs, most capable</p>
              </div>
            </div>
          </div>
          
          <p className="mt-8 text-xs text-[var(--text-mid)]/70">
            Ask an admin to connect a provider in{' '}
            <button onClick={() => navigate('/app/settings')} className="underline hover:text-[var(--gold)] transition-colors">
              Settings → General
            </button>
            , or set <code className="px-1.5 py-0.5 rounded bg-[var(--ink)]/5">RUNRIGHT_AI_PROVIDER</code> on self-hosted deployments
          </p>
        </div>
      </div>
    )
  }

  // Loading
  if (!status) {
    return (
      <div className="h-full flex items-center justify-center text-[var(--text-mid)]">
        Loading...
      </div>
    )
  }

  return (
    <div className="h-[calc(100vh-80px)] md:h-[calc(100vh-40px)] flex overflow-hidden -mx-4 md:-mx-9 -my-4 md:-my-10">
      {/* Sidebar */}
      <aside className={`
        flex-shrink-0 flex flex-col bg-[var(--ink)]/[0.03] border-r border-[var(--border)]
        transition-all duration-200 ease-out overflow-hidden
        ${sidebarOpen ? 'w-64' : 'w-0'}
      `}>
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-[var(--border)]">
          <span className="text-xs font-deco tracking-widest text-[var(--text-mid)] uppercase">History</span>
          <div className="flex items-center gap-1">
            {conversations.length > 0 && (
              <button
                onClick={handleClearAllHistory}
                className="w-7 h-7 rounded-md border border-[var(--border)] bg-transparent text-[var(--text-mid)] hover:text-[var(--text)] hover:border-[var(--border-dark)] transition-colors flex items-center justify-center"
                title="Clear all history"
              >
                <TrashIcon />
              </button>
            )}
            <button
              onClick={newConversation}
              className="w-7 h-7 rounded-md border border-[var(--border)] bg-transparent text-[var(--text-mid)] hover:text-[var(--gold)] hover:border-[var(--gold)]/50 transition-colors flex items-center justify-center"
              title="New conversation"
            >
              <PlusIcon />
            </button>
          </div>
        </div>

        {/* Conversations */}
        <div className="flex-1 overflow-y-auto p-2 space-y-1">
          {conversations.length === 0 ? (
            <p className="text-xs text-[var(--text-mid)]/60 text-center py-4">No conversations yet</p>
          ) : (
            conversations.map(conv => (
              <div
                key={conv.id}
                onClick={() => loadConversation(conv.id)}
                className={`
                  group flex items-center gap-2 px-3 py-2.5 rounded-lg cursor-pointer transition-colors
                  ${currentConvId === conv.id
                    ? 'bg-[var(--gold)]/10 text-[var(--text)]'
                    : 'text-[var(--text-mid)] hover:bg-ink/5 hover:text-[var(--text)]'}
                `}
              >
                <span className="flex-1 text-sm truncate">{conv.title || 'New conversation'}</span>
                <button
                  onClick={(e) => handleDeleteConversation(conv.id, e)}
                  className="opacity-0 group-hover:opacity-100 flex-shrink-0 p-1 rounded hover:bg-[var(--border)]/40 hover:text-[var(--text)] transition-all"
                  title="Delete"
                >
                  <TrashIcon />
                </button>
              </div>
            ))
          )}
        </div>

        {/* Stats */}
        {stats && (
          <div className="p-4 border-t border-[var(--border)] bg-[var(--ink)]/[0.02]">
            <p className="text-xs font-deco tracking-widest text-[var(--text-mid)]/70 uppercase mb-3">Quick Stats</p>
            <div className="space-y-2 text-sm">
              <div className="flex justify-between">
                <span className="text-[var(--text-mid)]">30d spend</span>
                <span className="text-[var(--text)] font-medium">{fmtCurrency(stats.total_cost_last_30d)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-[var(--text-mid)]">Potential savings</span>
                <span className="text-emerald-600 font-medium">{fmtCurrency(stats.potential_savings)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-[var(--text-mid)]">Avg CPU</span>
                <span className="text-[var(--text)]">{fmtPercent(stats.avg_cpu_utilization)}</span>
              </div>
            </div>
          </div>
        )}

        {/* Provider badge */}
        {status.provider && (
          <div className="px-4 py-3 border-t border-[var(--border)]">
            <div className="flex items-center gap-2 text-xs text-[var(--text-mid)]/70">
              <span className="w-2 h-2 rounded-full bg-emerald-500" />
              <span className="capitalize">{status.provider}</span>
              {status.model && <span className="text-[var(--text-mid)]/50">• {status.model}</span>}
            </div>
          </div>
        )}
      </aside>

      {/* Toggle button */}
      <button
        onClick={() => setSidebarOpen(p => !p)}
        className="flex-shrink-0 w-5 flex items-center justify-center bg-[var(--ink)]/[0.02] border-r border-[var(--border)] text-[var(--text-mid)]/50 hover:text-[var(--text-mid)] hover:bg-[var(--ink)]/5 transition-colors"
        title={sidebarOpen ? 'Hide sidebar' : 'Show sidebar'}
      >
        <ChevronIcon direction={sidebarOpen ? 'left' : 'right'} />
      </button>

      {/* Main chat */}
      <main className="flex-1 flex flex-col min-w-0 bg-[var(--cream)]">
        {/* Messages */}
        <div className="flex-1 overflow-y-auto">
          <div className="max-w-3xl mx-auto px-4 py-6">
            {messages.length === 0 ? (
              <div className="text-center py-16">
                <div className="w-14 h-14 mx-auto mb-5 rounded-xl bg-[var(--gold)]/10 flex items-center justify-center text-[var(--gold)]">
                  <SparkleIcon />
                </div>
                <h2 className="text-xl font-deco tracking-wide text-[var(--text)] mb-2">RunRight AI</h2>
                <p className="text-[var(--text-mid)] mb-8 max-w-md mx-auto">
                  Ask me about your CI/CD costs, resource usage, or optimization opportunities.
                </p>

                {suggestions.length > 0 && (
                  <div className="flex flex-wrap gap-2 justify-center">
                    {suggestions.slice(0, 4).map((q, i) => (
                      <button
                        key={i}
                        onClick={() => handleSend(q)}
                        className="px-4 py-2 rounded-full border border-[var(--border)] text-sm text-[var(--text-mid)] hover:text-[var(--text)] hover:border-[var(--gold)]/50 hover:bg-[var(--gold)]/5 transition-colors"
                      >
                        {q}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            ) : (
              <div className="space-y-6">
                {/* Memory banner — shown when older messages were compacted */}
                {currentConvSummary && (
                  <div className="rounded-xl border border-[var(--gold)]/20 bg-[var(--gold)]/5 overflow-hidden">
                    <button
                      onClick={() => setSummaryExpanded(p => !p)}
                      className="w-full flex items-center gap-2 px-4 py-2.5 text-left hover:bg-[var(--gold)]/10 transition-colors"
                    >
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-[var(--gold)] flex-shrink-0">
                        <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
                      </svg>
                      <span className="text-xs font-medium text-[var(--text-mid)] flex-1">
                        Older messages were summarized to save context
                      </span>
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"
                        className={`text-[var(--text-mid)]/60 transition-transform ${summaryExpanded ? 'rotate-180' : ''}`}>
                        <polyline points="6 9 12 15 18 9"/>
                      </svg>
                    </button>
                    {summaryExpanded && (
                      <div className="px-4 pb-3 pt-1 text-xs text-[var(--text-mid)] leading-relaxed border-t border-[var(--gold)]/10 whitespace-pre-wrap">
                        {currentConvSummary}
                      </div>
                    )}
                  </div>
                )}

                {messages.map((msg, msgIndex) => (
                  <div key={msg.id} className={`group flex gap-3 ${msg.role === 'user' ? 'justify-end' : ''}`}>
                    {msg.role !== 'user' && (
                      <div className="flex-shrink-0 w-8 h-8 rounded-lg bg-[var(--gold)]/10 flex items-center justify-center text-[var(--gold)]">
                        <BotIcon />
                      </div>
                    )}
                    {msg.role === 'user' && editingId === msg.id ? (
                      /* Editing mode */
                      <div className="max-w-[80%] w-full">
                        <textarea
                          value={editText}
                          onChange={e => setEditText(e.target.value)}
                          className="w-full px-4 py-3 rounded-xl border border-[var(--gold)]/50 bg-[var(--cream)] text-[var(--ink)] text-sm leading-relaxed resize-none focus:outline-none focus:ring-2 focus:ring-[var(--gold)]/20"
                          rows={Math.min(6, editText.split('\n').length + 1)}
                          autoFocus
                        />
                        <div className="flex justify-end gap-2 mt-2">
                          <button
                            onClick={handleCancelEdit}
                            className="px-3 py-1.5 text-xs text-[var(--text-mid)] hover:text-[var(--text)] transition-colors"
                          >
                            Cancel
                          </button>
                          <button
                            onClick={handleSaveEdit}
                            disabled={!editText.trim() || loading}
                            className="px-3 py-1.5 text-xs rounded-lg bg-[var(--gold)] text-[var(--ink)] hover:bg-[var(--gold-light)] disabled:opacity-50 transition-colors"
                          >
                            Save & Submit
                          </button>
                        </div>
                      </div>
                    ) : (
                      /* Normal display */
                      <>
                        {msg.role === 'user' && (
                          <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                            <button
                              onClick={() => handleEdit(msg.id)}
                              className="p-1.5 rounded-lg text-[var(--text-mid)]/60 hover:text-[var(--text)] hover:bg-[var(--ink)]/5 transition-colors"
                              title="Edit"
                            >
                              <EditIcon />
                            </button>
                            <button
                              onClick={() => handleRetry(msg.id)}
                              className="p-1.5 rounded-lg text-[var(--text-mid)]/60 hover:text-[var(--text)] hover:bg-[var(--ink)]/5 transition-colors"
                              title="Retry"
                            >
                              <RetryIcon />
                            </button>
                          </div>
                        )}
                        <div 
                          className={`
                          max-w-[80%] rounded-2xl px-4 py-3 text-sm leading-relaxed
                          ${msg.role === 'user'
                            ? 'bg-ink text-cream shadow-sm'
                            : 'bg-ink/5 text-[var(--text)]'}
                        `}
                          onClick={msg.role === 'assistant' ? handleMessageClick : undefined}
                        >
                          {renderMarkdown(msg.content)}
                        </div>
                      </>
                    )}
                  </div>
                ))}

                {loading && (
                  <div className="flex gap-3">
                    <div className="flex-shrink-0 w-8 h-8 rounded-lg bg-[var(--gold)]/10 flex items-center justify-center text-[var(--gold)]">
                      <BotIcon />
                    </div>
                    <div className="bg-ink/5 rounded-xl px-4 py-3">
                      <div className="flex items-center gap-2">
                        <span className="text-sm text-[var(--text-mid)]">Thinking</span>
                        <span className="flex gap-0.5">
                          <span className="w-1.5 h-1.5 rounded-full bg-[var(--gold)] animate-bounce" style={{ animationDelay: '0ms' }} />
                          <span className="w-1.5 h-1.5 rounded-full bg-[var(--gold)] animate-bounce" style={{ animationDelay: '150ms' }} />
                          <span className="w-1.5 h-1.5 rounded-full bg-[var(--gold)] animate-bounce" style={{ animationDelay: '300ms' }} />
                        </span>
                      </div>
                    </div>
                  </div>
                )}

                <div ref={messagesEndRef} />
              </div>
            )}
          </div>
        </div>

        {/* Data sources */}
        {dataSources.length > 0 && (
          <div className="px-4 py-2 border-t border-[var(--border)]/50">
            <div className="max-w-3xl mx-auto flex items-center gap-2 text-xs text-[var(--text-mid)]/60">
              <span>Data used:</span>
              {dataSources.map((ds, i) => (
                <span key={i} className="px-2 py-0.5 rounded bg-[var(--ink)]/5 border border-[var(--border)]/50">
                  {ds.type}{ds.count ? ` (${ds.count})` : ''}
                </span>
              ))}
            </div>
          </div>
        )}

        {/* Input */}
        <div className="border-t border-[var(--border)] bg-[var(--cream)] p-4">
          <div className="max-w-3xl mx-auto">
            <div className="flex gap-3 items-end">
              <textarea
                ref={inputRef}
                value={input}
                onChange={handleInputChange}
                onKeyDown={handleKeyDown}
                placeholder="Ask about your CI/CD costs..."
                rows={1}
                disabled={loading}
                className="flex-1 resize-none px-4 py-3 rounded-xl border border-[var(--border)] bg-[var(--cream)] text-[var(--ink)] placeholder:text-[var(--text-mid)] focus:outline-none focus:border-[var(--gold)]/50 focus:ring-2 focus:ring-[var(--gold)]/10 transition-all disabled:opacity-50"
                style={{ minHeight: '48px', maxHeight: '200px' }}
              />
              <button
                onClick={() => handleSend()}
                disabled={!input.trim() || loading}
                className="flex-shrink-0 w-12 h-12 rounded-xl bg-[var(--gold)] text-[var(--ink)] flex items-center justify-center hover:bg-[var(--gold-light)] transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <SendIcon />
              </button>
            </div>
            <p className="text-xs text-[var(--text-mid)]/50 text-center mt-2">
              Press Enter to send • Shift+Enter for new line
            </p>
          </div>
        </div>
      </main>

      {/* Confirm modal */}
      {confirmModal && (
        <ConfirmModal
          open
          title={confirmModal.title}
          message={confirmModal.message}
          confirmLabel="Delete"
          danger
          onConfirm={confirmModal.onConfirm}
          onCancel={() => setConfirmModal(null)}
        />
      )}
    </div>
  )
}
