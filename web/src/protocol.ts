export type Message =
  | { type: 'input'; data: string }
  | { type: 'output'; data: string }
  | { type: 'resize'; cols: number; rows: number }

// encodeInput takes the raw string from xterm.js onData and returns a JSON
// string ready to send over WebSocket.
// Uses TextEncoder so non-ASCII characters (multi-byte UTF-8) are handled
// correctly. btoa(str) would throw on anything outside Latin-1.
export function encodeInput(str: string): string {
  const bytes = new TextEncoder().encode(str)
  let binary = ''
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i])
  }
  const msg: Message = { type: 'input', data: btoa(binary) }
  return JSON.stringify(msg)
}

// decodeOutput takes an output Message and returns a Uint8Array of raw PTY
// bytes. Pass directly to terminal.write() to preserve the byte stream.
export function decodeOutput(msg: Extract<Message, { type: 'output' }>): Uint8Array {
  const binary = atob(msg.data)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}
