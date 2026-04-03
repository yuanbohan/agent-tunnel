import type { RelaySession } from './types'

type DashboardPreviewDisposer = () => void
type DashboardIntervalHandle = ReturnType<typeof setInterval>
type DashboardIntervalScheduler = (callback: () => void, intervalMs: number) => DashboardIntervalHandle
type DashboardIntervalClearer = (handle: DashboardIntervalHandle) => void

export interface DashboardViewDependencies {
  root: HTMLElement
  fetchSessions: () => Promise<RelaySession[]>
  renderSessionCard: (session: RelaySession) => string
  mountDashboardPreviews: (root: ParentNode) => Promise<DashboardPreviewDisposer>
  intervalMs?: number
  scheduleInterval?: DashboardIntervalScheduler
  clearScheduledInterval?: DashboardIntervalClearer
  onError?: (error: unknown) => void
}

export interface DashboardViewController {
  ready: Promise<void>
  refresh: () => Promise<void>
  dispose: () => void
}

export function startDashboardView(deps: DashboardViewDependencies): DashboardViewController {
  const intervalMs = deps.intervalMs ?? 5000
  const scheduleInterval = deps.scheduleInterval ?? setInterval
  const clearScheduledInterval = deps.clearScheduledInterval ?? clearInterval
  const onError = deps.onError ?? console.error

  let currentPreviewDisposer: DashboardPreviewDisposer | null = null
  let intervalHandle: DashboardIntervalHandle | null = null
  let refreshing = false
  let disposed = false
  let hasRenderedContent = false

  const refresh = async () => {
    if (disposed || refreshing) {
      return
    }

    refreshing = true
    try {
      const sessions = await deps.fetchSessions()
      if (disposed) {
        return
      }

      if (currentPreviewDisposer) {
        currentPreviewDisposer()
        currentPreviewDisposer = null
      }

      if (sessions.length === 0) {
        deps.root.innerHTML = `<div class="relay-placeholder">No live sessions right now.</div>`
        hasRenderedContent = true
        return
      }

      deps.root.innerHTML = sessions.map(deps.renderSessionCard).join('')
      hasRenderedContent = true

      try {
        const disposer = await deps.mountDashboardPreviews(deps.root)
        if (disposed) {
          disposer()
          return
        }
        currentPreviewDisposer = disposer
      } catch (error) {
        onError(error)
      }
    } catch (error) {
      onError(error)
      if (!hasRenderedContent) {
        deps.root.innerHTML = `<div class="relay-placeholder">Failed to load sessions.</div>`
      }
    } finally {
      refreshing = false
    }
  }

  intervalHandle = scheduleInterval(() => {
    void refresh()
  }, intervalMs)

  const ready = refresh()

  return {
    ready,
    refresh,
    dispose() {
      disposed = true
      if (intervalHandle !== null) {
        clearScheduledInterval(intervalHandle)
        intervalHandle = null
      }
      if (currentPreviewDisposer) {
        currentPreviewDisposer()
        currentPreviewDisposer = null
      }
    },
  }
}
