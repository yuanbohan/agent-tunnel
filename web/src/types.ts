export type RelaySession = {
  session_id: string
  launcher: string
  label?: string
  cwd: string
  command_preview: string
  started_at: string
  last_preview?: string
  latest_seq?: number
  last_read_seq?: number
  unread_count?: number
  last_active_at?: string
  preview_seq?: number
  preview_b64?: string
}

export type HistoryMessage = {
  seq: number
  data_b64: string
}

export type HistoryPage = {
  messages: HistoryMessage[]
  has_more: boolean
  latest_seq: number
  last_read_seq: number
}

export type SessionReadState = {
  session_id: string
  latest_seq: number
  last_read_seq: number
  unread_count: number
}
