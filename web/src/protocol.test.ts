import { describe, it, expect } from 'vitest'
import { encodeInput, decodeOutput } from './protocol'

describe('encodeInput', () => {
  it('encodes ASCII input to a JSON input message', () => {
    const msg = encodeInput('ls\r')
    const parsed = JSON.parse(msg)
    expect(parsed.type).toBe('input')
    // base64 of UTF-8 bytes for "ls\r"
    expect(parsed.data).toBe(btoa('ls\r'))
  })

  it('encodes non-ASCII input (é) without throwing', () => {
    const msg = encodeInput('é')
    const parsed = JSON.parse(msg)
    expect(parsed.type).toBe('input')
    // "é" is 0xc3 0xa9 in UTF-8, base64 = "w6k="
    expect(parsed.data).toBe('w6k=')
  })
})

describe('decodeOutput', () => {
  it('decodes a base64 output message to Uint8Array', () => {
    const msg = JSON.stringify({ type: 'output', data: btoa('hello') })
    const bytes = decodeOutput(JSON.parse(msg))
    expect(bytes).toBeInstanceOf(Uint8Array)
    expect(Array.from(bytes)).toEqual([104, 101, 108, 108, 111])
  })
})
