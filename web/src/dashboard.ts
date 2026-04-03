import type { RelaySession } from './types'

export function renderSessionCard(session: RelaySession): string {
  const title = sessionTitle(session)
  const launcher = launcherName(session.launcher)
  const assetPath = launcherAssetPath(session.launcher)
  const unreadCount = session.unread_count ?? 0

  return `
    <a
      class="session-card"
      href="/sessions/${encodeURIComponent(session.session_id)}"
      title="${escapeHTML(session.cwd)}"
    >
      <div class="session-card__row">
        <div class="session-card__identity">
          <span class="session-card__icon">
            ${assetPath
              ? `<img class="session-card__icon-image" src="${assetPath}" alt="" loading="lazy" decoding="async">`
              : `<span class="session-card__icon-fallback">${escapeHTML(launcherInitials(launcher))}</span>`}
          </span>
          <div class="session-card__identity-copy">
            <div class="session-card__title">${escapeHTML(title)}</div>
            <div class="session-card__launcher">${escapeHTML(launcher)}</div>
          </div>
        </div>
        <div class="session-card__meta">
          <div class="session-card__time">${escapeHTML(formatRelativeTime(session.last_active_at))}</div>
          ${unreadCount > 0
            ? `<div class="session-card__badge">${escapeHTML(`${unreadCount} unread`)}</div>`
            : ''}
        </div>
      </div>
      ${renderPreview(session)}
    </a>
  `
}

export function launcherName(launcher: string): string {
  switch (launcher.trim().toLowerCase()) {
    case 'codex':
    case 'openai':
      return 'OpenAI'
    case 'gemini':
      return 'Gemini'
    case 'claude':
      return 'Claude'
    default: {
      const trimmed = launcher.trim()
      if (trimmed === '') {
        return 'Unknown'
      }
      return trimmed.charAt(0).toUpperCase() + trimmed.slice(1)
    }
  }
}

export function launcherAssetPath(launcher: string): string {
  switch (launcherName(launcher)) {
    case 'OpenAI':
      return '/launchers/openai.ico'
    case 'Gemini':
      return '/launchers/gemini.png'
    case 'Claude':
      return '/launchers/claude.ico'
    default:
      return ''
  }
}

export function sessionTitle(session: RelaySession): string {
  const label = session.label?.trim()
  if (label) {
    return label
  }

  const base = basename(session.cwd)
  if (base) {
    return base
  }

  return 'Session'
}

export function formatRelativeTime(value?: string): string {
  if (!value) {
    return '--'
  }

  const timestamp = Date.parse(value)
  if (Number.isNaN(timestamp)) {
    return '--'
  }

  const deltaSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (deltaSeconds < 60) {
    return 'now'
  }
  if (deltaSeconds < 3600) {
    return `${Math.floor(deltaSeconds / 60)}m`
  }
  if (deltaSeconds < 86400) {
    return `${Math.floor(deltaSeconds / 3600)}h`
  }
  return `${Math.floor(deltaSeconds / 86400)}d`
}

function renderPreview(session: RelaySession): string {
  if (!session.preview_b64) {
    return `
      <div class="session-card__preview session-card__preview--empty">
        <span class="session-card__preview-placeholder">No output yet</span>
      </div>
    `
  }

  return `
    <div class="session-card__preview" aria-hidden="true">
      <div
        class="session-card__preview-terminal"
        data-dashboard-preview
        data-preview-b64="${escapeHTML(session.preview_b64)}"
        data-preview-seq="${session.preview_seq ?? ''}"
      ></div>
    </div>
  `
}

function basename(value: string): string {
  const normalized = value.replace(/[\\/]+$/, '')
  const parts = normalized.split(/[\\/]/)
  return parts[parts.length - 1] ?? ''
}

function launcherInitials(launcher: string): string {
  return launcherName(launcher)
    .split(/\s+/)
    .map((part) => part.charAt(0))
    .join('')
    .slice(0, 2)
    .toUpperCase()
}

function escapeHTML(value: string): string {
  return value
    .split('&').join('&amp;')
    .split('<').join('&lt;')
    .split('>').join('&gt;')
    .split('"').join('&quot;')
}
