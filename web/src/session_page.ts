import { decodeOutput, type Message } from './protocol'
import type { HistoryPage, HistoryMessage } from './types'

export function nextInputState(current: boolean): boolean {
  return !current
}

export function stateChipLabel(enabled: boolean): string {
  return enabled ? 'Input on' : 'Read-only'
}

export function stateChipClass(enabled: boolean): string {
  return enabled ? 'input-chip input-chip--enabled' : 'input-chip'
}

export function unreadJumpLabel(count: number): string {
  return `Jump to ${count} unread`
}

export function firstUnreadSeq(lastReadSeq: number, latestSeq: number): number | null {
  if (latestSeq <= lastReadSeq) {
    return null
  }
  return lastReadSeq + 1
}

export type SessionPageFrame = {
  seq: number
  data: Uint8Array
}

export interface SessionPageTerminal {
  replaceFrames(frames: SessionPageFrame[], anchorSeq?: number): Promise<void> | void
  appendFrame(frame: SessionPageFrame): Promise<void> | void
  scrollToSeq(seq: number): boolean
  highlightSeq(seq: number): boolean
}

export interface SessionPageControllerHandle {
  init(): Promise<void>
  loadOlderHistory(): Promise<boolean>
  appendLiveOutput(msg: Extract<Message, { type: 'output' }>): Promise<void>
  jumpToFirstUnread(): Promise<JumpResult>
  getUnreadJumpState(): UnreadJumpState
}

type SessionPageControllerOptions = {
  sessionId: string
  terminal: SessionPageTerminal
  fetchHistory: (sessionId: string, before?: number, after?: number) => Promise<HistoryPage>
  markRead: (sessionId: string, seq: number) => Promise<void>
  attachLive: () => Promise<void>
}

export type UnreadJumpState = {
  visible: boolean
  label: string
  targetSeq: number | null
}

export type JumpResult = 'target' | 'oldest' | 'none'

export function createSessionPageController(options: SessionPageControllerOptions): SessionPageControllerHandle {
  return new SessionPageController(options)
}

class SessionPageController implements SessionPageControllerHandle {
  private readonly sessionId: string
  private readonly terminal: SessionPageTerminal
  private readonly fetchHistory: SessionPageControllerOptions['fetchHistory']
  private readonly markRead: SessionPageControllerOptions['markRead']
  private readonly attachLive: SessionPageControllerOptions['attachLive']
  private frames: SessionPageFrame[] = []
  private hasMoreHistory = false
  private initialLatestSeq = 0
  private initialLastReadSeq = 0
  private latestSeq = 0
  private jumped = false
  private loadingOlderPromise: Promise<boolean> | null = null
  private syncingGap = false
  private pendingLiveOutputs: Extract<Message, { type: 'output' }>[] = []
  private loadedSeqs = new Set<number>()

  constructor(options: SessionPageControllerOptions) {
    this.sessionId = options.sessionId
    this.terminal = options.terminal
    this.fetchHistory = options.fetchHistory
    this.markRead = options.markRead
    this.attachLive = options.attachLive
  }

  async init(): Promise<void> {
    const page = await this.fetchHistory(this.sessionId)
    this.initialLatestSeq = page.latest_seq
    this.initialLastReadSeq = page.last_read_seq
    this.latestSeq = page.latest_seq
    this.hasMoreHistory = page.has_more
    this.frames = this.decodeHistoryMessages(page.messages)
    this.loadedSeqs = new Set(this.frames.map((frame) => frame.seq))

    await this.terminal.replaceFrames(this.frames)
    this.syncingGap = true
    await this.attachLive()
    try {
      await this.loadGapHistory()
      await this.flushPendingLiveOutputs()
    } finally {
      this.syncingGap = false
    }
    await this.markRead(this.sessionId, this.latestSeq)
  }

  async loadOlderHistory(): Promise<boolean> {
    if (this.frames.length === 0 || !this.hasMoreHistory) {
      return false
    }
    if (this.loadingOlderPromise) {
      return this.loadingOlderPromise
    }

    this.loadingOlderPromise = (async () => {
      const before = this.frames[0]!.seq
      const page = await this.fetchHistory(this.sessionId, before)
      this.hasMoreHistory = page.has_more
      if (page.messages.length === 0) {
        return false
      }

      const olderFrames = this.decodeHistoryMessages(page.messages)
      const anchorSeq = this.frames[0]!.seq
      this.frames = [...olderFrames, ...this.frames]
      for (const frame of olderFrames) {
        this.loadedSeqs.add(frame.seq)
      }
      await this.terminal.replaceFrames(this.frames, anchorSeq)
      return true
    })()

    try {
      return await this.loadingOlderPromise
    } finally {
      this.loadingOlderPromise = null
    }
  }

  async appendLiveOutput(msg: Extract<Message, { type: 'output' }>): Promise<void> {
    if (this.syncingGap) {
      this.pendingLiveOutputs.push(msg)
      return
    }
    await this.appendLiveOutputFrame(msg)
  }

  async jumpToFirstUnread(): Promise<JumpResult> {
    const targetSeq = firstUnreadSeq(this.initialLastReadSeq, this.initialLatestSeq)
    if (targetSeq === null) {
      return 'none'
    }

    while (!this.isLoaded(targetSeq) && this.hasMoreHistory) {
      const loaded = await this.loadOlderHistory()
      if (!loaded) {
        break
      }
    }

    if (this.isLoaded(targetSeq)) {
      this.jumped = true
      if (!this.terminal.scrollToSeq(targetSeq)) {
        return 'none'
      }
      this.terminal.highlightSeq(targetSeq)
      return 'target'
    }

    if (this.frames.length > 0) {
      this.jumped = true
      const oldestSeq = this.frames[0]!.seq
      if (!this.terminal.scrollToSeq(oldestSeq)) {
        return 'none'
      }
      this.terminal.highlightSeq(oldestSeq)
      return 'oldest'
    }

    return 'none'
  }

  getUnreadJumpState(): UnreadJumpState {
    const unreadCount = this.initialLatestSeq > this.initialLastReadSeq
      ? this.initialLatestSeq - this.initialLastReadSeq
      : 0
    return {
      visible: unreadCount > 0 && !this.jumped,
      label: unreadJumpLabel(unreadCount),
      targetSeq: firstUnreadSeq(this.initialLastReadSeq, this.initialLatestSeq),
    }
  }

  private async loadGapHistory(): Promise<void> {
    const upperBound = this.initialLatestSeq
    let after = this.frames.length > 0 ? this.frames[this.frames.length - 1]!.seq : 0
    if (after >= upperBound) {
      return
    }

    while (after < upperBound) {
      const page = await this.fetchHistory(this.sessionId, upperBound + 1, after)
      if (page.messages.length === 0) {
        return
      }

      const gapFrames = this.decodeHistoryMessages(page.messages)
      for (const frame of gapFrames) {
        await this.appendHistoryFrame(frame)
      }

      after = page.messages[page.messages.length - 1]!.seq
      if (!page.has_more) {
        return
      }
    }
  }

  private async flushPendingLiveOutputs(): Promise<void> {
    while (this.pendingLiveOutputs.length > 0) {
      const pending = this.pendingLiveOutputs
      this.pendingLiveOutputs = []
      pending.sort((a, b) => a.seq - b.seq)
      for (const msg of pending) {
        await this.appendLiveOutputFrame(msg)
      }
    }
  }

  private async appendLiveOutputFrame(msg: Extract<Message, { type: 'output' }>): Promise<void> {
    const frame = {
      seq: msg.seq,
      data: decodeOutput(msg),
    }
    if (this.loadedSeqs.has(frame.seq)) {
      return
    }
    this.latestSeq = Math.max(this.latestSeq, frame.seq)
    this.frames.push(frame)
    this.loadedSeqs.add(frame.seq)
    await this.terminal.appendFrame(frame)
  }

  private decodeHistoryMessages(messages: HistoryMessage[]): SessionPageFrame[] {
    return messages.map((message) => ({
      seq: message.seq,
      data: decodeOutput({
        type: 'output',
        seq: message.seq,
        data: message.data_b64,
      }),
    }))
  }

  private async appendHistoryFrame(frame: SessionPageFrame): Promise<void> {
    if (this.loadedSeqs.has(frame.seq)) {
      return
    }
    this.latestSeq = Math.max(this.latestSeq, frame.seq)
    this.frames.push(frame)
    this.loadedSeqs.add(frame.seq)
    await this.terminal.appendFrame(frame)
  }

  private isLoaded(seq: number): boolean {
    return this.loadedSeqs.has(seq)
  }
}
