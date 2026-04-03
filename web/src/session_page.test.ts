import { describe, expect, it } from 'vitest'
import {
  createSessionPageController,
  firstUnreadSeq,
  nextInputState,
  stateChipLabel,
  unreadJumpLabel,
  type SessionPageTerminal,
} from './session_page'
import type { HistoryPage } from './types'

function historyPage(
  messages: Array<[number, string]>,
  latestSeq: number,
  lastReadSeq: number,
  hasMore: boolean,
): HistoryPage {
  return {
    messages: messages.map(([seq, data]) => ({
      seq,
      data_b64: btoa(data),
    })),
    latest_seq: latestSeq,
    last_read_seq: lastReadSeq,
    has_more: hasMore,
  }
}

function createTerminalStub(events: string[]): SessionPageTerminal {
  return {
    async replaceFrames(frames, anchorSeq) {
      const rendered = frames.map((frame) => frame.seq).join(',')
      events.push(`render:${rendered}:${anchorSeq ?? 'none'}`)
    },
    async appendFrame(frame) {
      events.push(`append:${frame.seq}`)
    },
    scrollToSeq(seq) {
      events.push(`scroll:${seq}`)
      return true
    },
    highlightSeq(seq) {
      events.push(`highlight:${seq}`)
      return true
    },
  }
}

describe('relay session page', () => {
  it('renders initial history, bridges the websocket gap, and marks read after syncing', async () => {
    const events: string[] = []
    let resolveAttach = () => {}
    let resolveGap = () => {}

    const controller = createSessionPageController({
      sessionId: 'sess-1',
      terminal: createTerminalStub(events),
      fetchHistory: async (sessionId, before, after) => {
        events.push(`history:${sessionId}:${before ?? 'latest'}:${after ?? 'none'}`)
        if (before === undefined && after === undefined) {
          return historyPage([[4, 'four'], [5, 'five']], 6, 3, true)
        }
        if (before === 7 && after === 5) {
          await new Promise<void>((resolve) => {
            resolveGap = resolve
          })
          return historyPage([[6, 'six']], 6, 3, false)
        }
        throw new Error(`unexpected history request: before=${before} after=${after}`)
      },
      markRead: async (sessionId, seq) => {
        events.push(`read:${sessionId}:${seq}`)
      },
      attachLive: async () => {
        events.push('attach')
        await new Promise<void>((resolve) => {
          resolveAttach = resolve
        })
      },
    })

    const initPromise = controller.init()
    await Promise.resolve()
    await Promise.resolve()

    expect(events).toEqual([
      'history:sess-1:latest:none',
      'render:4,5:none',
      'attach',
    ])

    void controller.appendLiveOutput({
      type: 'output',
      seq: 6,
      data: btoa('six'),
    })
    void controller.appendLiveOutput({
      type: 'output',
      seq: 7,
      data: btoa('seven'),
    })

    resolveAttach()
    await Promise.resolve()
    await Promise.resolve()
    resolveGap()
    await initPromise

    expect(events).toEqual([
      'history:sess-1:latest:none',
      'render:4,5:none',
      'attach',
      'history:sess-1:7:5',
      'append:6',
      'append:7',
      'read:sess-1:7',
    ])
  })

  it('waits for the initial history render before starting live attach', async () => {
    const events: string[] = []
    let resolveRender = () => {}

    const controller = createSessionPageController({
      sessionId: 'sess-1',
      terminal: {
        replaceFrames() {
          events.push('render:start')
          return new Promise<void>((resolve) => {
            resolveRender = () => {
              events.push('render:done')
              resolve()
            }
          })
        },
        async appendFrame() {},
        scrollToSeq() {
          return true
        },
        highlightSeq() {
          return true
        },
      },
      fetchHistory: async () => {
        events.push('history')
        return historyPage([[4, 'four'], [5, 'five']], 5, 3, false)
      },
      markRead: async () => {
        events.push('read')
      },
      attachLive: async () => {
        events.push('attach')
      },
    })

    const initPromise = controller.init()
    await Promise.resolve()

    expect(events).toEqual([
      'history',
      'render:start',
    ])

    resolveRender()
    await initPromise

    expect(events).toEqual([
      'history',
      'render:start',
      'render:done',
      'attach',
      'read',
    ])
  })

  it('prepends older history in chronological order when paging upward', async () => {
    const events: string[] = []
    const requests: Array<number | undefined> = []

    const controller = createSessionPageController({
      sessionId: 'sess-1',
      terminal: createTerminalStub(events),
      fetchHistory: async (_sessionId, before) => {
        requests.push(before)
        if (before === undefined) {
          return historyPage([[4, 'four'], [5, 'five']], 5, 3, true)
        }
        if (before === 4) {
          return historyPage([[2, 'two'], [3, 'three']], 5, 3, true)
        }
        return historyPage([[1, 'one']], 5, 3, false)
      },
      markRead: async () => {},
      attachLive: async () => {},
    })

    await controller.init()
    await controller.loadOlderHistory()
    await controller.loadOlderHistory()

    expect(requests).toEqual([undefined, 4, 2])
    expect(events).toEqual([
      'render:4,5:none',
      'render:2,3,4,5:4',
      'render:1,2,3,4,5:2',
    ])
  })

  it('keeps paging backward until the first unread frame is loaded', async () => {
    const events: string[] = []
    const requests: Array<number | undefined> = []

    const controller = createSessionPageController({
      sessionId: 'sess-1',
      terminal: createTerminalStub(events),
      fetchHistory: async (_sessionId, before) => {
        requests.push(before)
        if (before === undefined) {
          return historyPage([[4, 'four'], [5, 'five']], 5, 1, true)
        }
        if (before === 4) {
          return historyPage([[2, 'two'], [3, 'three']], 5, 1, true)
        }
        return historyPage([[1, 'one']], 5, 1, false)
      },
      markRead: async () => {},
      attachLive: async () => {},
    })

    await controller.init()

    expect(controller.getUnreadJumpState()).toEqual({
      visible: true,
      label: 'Jump to 4 unread',
      targetSeq: 2,
    })

    await expect(controller.jumpToFirstUnread()).resolves.toBe('target')

    expect(requests).toEqual([undefined, 4])
    expect(events).toEqual([
      'render:4,5:none',
      'render:2,3,4,5:4',
      'scroll:2',
      'highlight:2',
    ])
    expect(controller.getUnreadJumpState().visible).toBe(false)
  })

  it('waits for an in-flight older-history load before deciding unread history is unavailable', async () => {
    const events: string[] = []
    let resolveOlder = () => {}

    const controller = createSessionPageController({
      sessionId: 'sess-1',
      terminal: createTerminalStub(events),
      fetchHistory: async (_sessionId, before) => {
        if (before === undefined) {
          return historyPage([[4, 'four'], [5, 'five']], 5, 1, true)
        }
        if (before === 4) {
          await new Promise<void>((resolve) => {
            resolveOlder = resolve
          })
          return historyPage([[2, 'two'], [3, 'three']], 5, 1, false)
        }
        throw new Error(`unexpected before=${before}`)
      },
      markRead: async () => {},
      attachLive: async () => {},
    })

    await controller.init()
    const pagingPromise = controller.loadOlderHistory()
    const jumpPromise = controller.jumpToFirstUnread()

    await Promise.resolve()
    resolveOlder()

    await expect(pagingPromise).resolves.toBe(true)
    await expect(jumpPromise).resolves.toBe('target')
    expect(events).toEqual([
      'render:4,5:none',
      'render:2,3,4,5:4',
      'scroll:2',
      'highlight:2',
    ])
  })

  it('reports when the unread target has already been evicted', async () => {
    const events: string[] = []

    const controller = createSessionPageController({
      sessionId: 'sess-1',
      terminal: createTerminalStub(events),
      fetchHistory: async () => historyPage([[8, 'eight'], [9, 'nine']], 9, 2, false),
      markRead: async () => {},
      attachLive: async () => {},
    })

    await controller.init()

    await expect(controller.jumpToFirstUnread()).resolves.toBe('oldest')
    expect(events).toEqual([
      'render:8,9:none',
      'scroll:8',
      'highlight:8',
    ])
  })

  it('builds unread labels and preserves the input toggle behavior', () => {
    expect(unreadJumpLabel(12)).toBe('Jump to 12 unread')
    expect(firstUnreadSeq(3, 5)).toBe(4)
    expect(firstUnreadSeq(5, 5)).toBeNull()

    const next = nextInputState(false)
    expect(next).toBe(true)
    expect(stateChipLabel(false)).toBe('Read-only')
    expect(stateChipLabel(true)).toBe('Input on')
  })
})
