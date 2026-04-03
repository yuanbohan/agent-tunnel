import type { RelaySession } from './types'

export function renderSessionCard(session: RelaySession): string {
  const title = session.label?.trim() || launcherName(session.launcher)
  const launcher = launcherName(session.launcher)
  const preview = session.last_preview?.trim() || 'No preview yet'

  return `
    <a class="session-card" href="/sessions/${encodeURIComponent(session.session_id)}">
      <div class="session-card__row">
        <div class="session-card__identity">
          <span class="session-card__icon">${launcherIcon(session.launcher)}</span>
          <div class="session-card__identity-copy">
            <div class="session-card__title">${escapeHTML(title)}</div>
            <div class="session-card__launcher">${escapeHTML(launcher)}</div>
          </div>
        </div>
        <div class="session-card__time">${escapeHTML(formatRelativeTime(session.last_active_at))}</div>
      </div>
      <div class="session-card__command">${escapeHTML(session.command_preview)}</div>
      <div class="session-card__cwd">${escapeHTML(session.cwd)}</div>
      <div class="session-card__preview">${escapeHTML(preview)}</div>
    </a>
  `
}

export function launcherIcon(launcher: string): string {
  switch (launcher.trim().toLowerCase()) {
    case 'codex':
      return 'CX'
    case 'gemini':
      return 'GM'
    case 'claude':
      return 'CL'
    default:
      return '--'
  }
}

export function launcherName(launcher: string): string {
  const trimmed = launcher.trim()
  if (trimmed === '') {
    return 'Unknown'
  }
  return trimmed.charAt(0).toUpperCase() + trimmed.slice(1)
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

function escapeHTML(value: string): string {
  return value
    .split('&').join('&amp;')
    .split('<').join('&lt;')
    .split('>').join('&gt;')
    .split('"').join('&quot;')
}
