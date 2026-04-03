import { describe, expect, it } from 'vitest'
import { renderSessionCard } from './dashboard'
import { startDashboardView } from './dashboard_view'
import type { RelaySession } from './types'

function session(session_id: string, overrides: Partial<RelaySession> = {}): RelaySession {
  return {
    session_id,
    launcher: 'codex',
    cwd: `/tmp/${session_id}`,
    command_preview: 'codex',
    started_at: '2026-04-02T12:00:00Z',
    last_active_at: '2026-04-02T12:01:00Z',
    ...overrides,
  }
}

function createRootStub() {
  let markup = ''
  return {
    get innerHTML() {
      return markup
    },
    set innerHTML(value: string) {
      markup = value
    },
    querySelectorAll(selector: string) {
      if (selector !== '[data-dashboard-preview]') {
        return []
      }

      const count = markup.split('data-dashboard-preview').length - 1
      return Array.from({ length: count }, () => ({}))
    },
  } as unknown as HTMLElement
}

describe('startDashboardView', () => {
  it('polls, disposes the previous preview mount, and remounts previews after refreshes', async () => {
    const root = createRootStub()
    const events: string[] = []
    let fetchCount = 0
    let scheduledCallback: (() => void) | undefined

    const view = startDashboardView({
      root,
      fetchSessions: async () => {
        fetchCount += 1
        events.push(`fetch:${fetchCount}`)
        return fetchCount === 1
          ? [session('sess-1', { preview_b64: btoa('frame-1') })]
          : [
              session('sess-2', { preview_b64: btoa('frame-2') }),
              session('sess-3', { preview_b64: btoa('frame-3') }),
            ]
      },
      renderSessionCard,
      mountDashboardPreviews: async (mountRoot) => {
        const previewCount = mountRoot.querySelectorAll('[data-dashboard-preview]').length
        events.push(`mount:${previewCount}`)
        return () => {
          events.push(`dispose:${previewCount}`)
        }
      },
      scheduleInterval: (callback, intervalMs) => {
        events.push(`interval:${intervalMs}`)
        scheduledCallback = callback
        return 1 as ReturnType<typeof setInterval>
      },
      clearScheduledInterval: () => {
        events.push('clear-interval')
      },
      intervalMs: 5000,
      onError: (error) => {
        events.push(`error:${String(error)}`)
      },
    })

    await view.ready
    expect(events).toEqual(['interval:5000', 'fetch:1', 'mount:1'])
    expect(root.innerHTML).toContain('sess-1')

    await view.refresh()

    expect(scheduledCallback).toBeDefined()
    expect(events).toEqual([
      'interval:5000',
      'fetch:1',
      'mount:1',
      'fetch:2',
      'dispose:1',
      'mount:2',
    ])
    expect(root.innerHTML).toContain('sess-2')
    expect(root.innerHTML).toContain('sess-3')

    view.dispose()

    expect(events).toEqual([
      'interval:5000',
      'fetch:1',
      'mount:1',
      'fetch:2',
      'dispose:1',
      'mount:2',
      'clear-interval',
      'dispose:2',
    ])
  })

  it('skips overlapping refreshes when a poll is already in flight', async () => {
    const root = createRootStub()
    const events: string[] = []
    let resolveFetch: ((sessions: RelaySession[]) => void) | undefined
    let fetchCalls = 0

    const view = startDashboardView({
      root,
      fetchSessions: async () => {
        fetchCalls += 1
        events.push(`fetch:${fetchCalls}`)
        return await new Promise<RelaySession[]>((resolve) => {
          resolveFetch = resolve
        })
      },
      renderSessionCard,
      mountDashboardPreviews: async () => {
        events.push('mount')
        return () => {
          events.push('dispose')
        }
      },
      scheduleInterval: (callback) => {
        events.push('interval')
        void callback
        return 1 as ReturnType<typeof setInterval>
      },
    })

    void view.refresh()
    await Promise.resolve()

    expect(fetchCalls).toBe(1)

    await view.refresh()
    expect(fetchCalls).toBe(1)

    resolveFetch?.([session('sess-1')])
    await view.ready

    expect(events).toEqual(['interval', 'fetch:1', 'mount'])
  })

  it('disposes preview terminals even if the mount resolves after teardown', async () => {
    const root = createRootStub()
    const events: string[] = []
    let resolveMount: ((dispose: () => void) => void) | undefined
    let mountStarted = () => {}

    const view = startDashboardView({
      root,
      fetchSessions: async () => [session('sess-1', { preview_b64: btoa('frame-1') })],
      renderSessionCard,
      mountDashboardPreviews: async () => {
        events.push('mount:start')
        mountStarted()
        return await new Promise<() => void>((resolve) => {
          resolveMount = resolve
        })
      },
      scheduleInterval: () => 1 as ReturnType<typeof setInterval>,
      clearScheduledInterval: () => {
        events.push('clear-interval')
      },
    })

    await new Promise<void>((resolve) => {
      mountStarted = resolve
    })
    view.dispose()
    resolveMount?.(() => {
      events.push('dispose:late-mount')
    })
    await view.ready

    expect(events).toEqual([
      'mount:start',
      'clear-interval',
      'dispose:late-mount',
    ])
  })
})
