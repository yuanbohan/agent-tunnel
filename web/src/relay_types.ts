export type RelaySession = {
  session_id: string
  launcher: string
  label?: string
  cwd: string
  command_preview: string
  started_at: string
  last_preview?: string
  last_active_at?: string
}
