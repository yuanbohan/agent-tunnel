export type Message =
  | { type: 'input'; data: string }
  | { type: 'output'; data: string }
  | { type: 'resize'; cols: number; rows: number }

const encoder = new TextEncoder()

// encodeInput takes the raw string from xterm.js onData and returns a JSON
// string ready to send over WebSocket.
// Uses TextEncoder so non-ASCII characters (multi-byte UTF-8) are handled
// correctly. btoa(str) would throw on anything outside Latin-1.
export function encodeInput(str: string): string {
  const bytes = encoder.encode(str)
  const binary = Array.from(bytes, b => String.fromCharCode(b)).join('')
  const msg: Message = { type: 'input', data: btoa(binary) }
  return JSON.stringify(msg)
}

// decodeOutput takes an output Message and returns a Uint8Array of raw PTY
// bytes. Pass directly to terminal.write() to preserve the byte stream.
export function decodeOutput(msg: Extract<Message, { type: 'output' }>): Uint8Array {
  try {
    const binary = atob(msg.data)
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i)
    }
    return bytes
  } catch {
    return new Uint8Array(0)
  }
}
