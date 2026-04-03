import { describe, it, expect } from 'vitest'
import { isTerminalAutoResponse } from './input_filter'

describe('isTerminalAutoResponse', () => {
  describe('filters terminal auto-responses', () => {
    it('filters Cursor Position Report (CPR)', () => {
      expect(isTerminalAutoResponse('\x1b[24;80R')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[1;1R')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[999;999R')).toBe(true)
    })

    it('filters Extended CPR (DECXCPR)', () => {
      expect(isTerminalAutoResponse('\x1b[?24;80R')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[?1;1R')).toBe(true)
    })

    it('filters Primary Device Attributes (DA1)', () => {
      expect(isTerminalAutoResponse('\x1b[?1;2c')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[?62;1;2;6;7;8;9;15c')).toBe(true)
    })

    it('filters Secondary Device Attributes (DA2)', () => {
      expect(isTerminalAutoResponse('\x1b[>0;0;0c')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[>1;95;0c')).toBe(true)
    })

    it('filters foreground color query response (OSC 10, ST terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]10;rgb:ffff/ffff/ffff\x1b\\')).toBe(true)
    })

    it('filters foreground color query response (OSC 10, BEL terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]10;rgb:ffff/ffff/ffff\x07')).toBe(true)
    })

    it('filters background color query response (OSC 11, ST terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]11;rgb:0000/0000/0000\x1b\\')).toBe(true)
    })

    it('filters background color query response (OSC 11, BEL terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]11;rgb:0000/0000/0000\x07')).toBe(true)
    })
  })

  describe('passes through user input', () => {
    it('passes regular characters', () => {
      expect(isTerminalAutoResponse('a')).toBe(false)
      expect(isTerminalAutoResponse('Y')).toBe(false)
      expect(isTerminalAutoResponse('n')).toBe(false)
      expect(isTerminalAutoResponse('hello')).toBe(false)
    })

    it('passes Enter, Tab, Escape, Space', () => {
      expect(isTerminalAutoResponse('\r')).toBe(false)
      expect(isTerminalAutoResponse('\t')).toBe(false)
      expect(isTerminalAutoResponse('\x1b')).toBe(false)
      expect(isTerminalAutoResponse(' ')).toBe(false)
    })

    it('passes arrow keys', () => {
      expect(isTerminalAutoResponse('\x1b[A')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[B')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[C')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[D')).toBe(false)
    })

    it('passes modified arrow keys (Ctrl+arrow)', () => {
      expect(isTerminalAutoResponse('\x1b[1;5A')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[1;5C')).toBe(false)
    })

    it('passes application cursor mode arrow keys', () => {
      expect(isTerminalAutoResponse('\x1bOA')).toBe(false)
      expect(isTerminalAutoResponse('\x1bOB')).toBe(false)
      expect(isTerminalAutoResponse('\x1bOC')).toBe(false)
      expect(isTerminalAutoResponse('\x1bOD')).toBe(false)
    })

    it('passes function keys', () => {
      expect(isTerminalAutoResponse('\x1bOP')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[15~')).toBe(false)
    })

    it('passes control characters', () => {
      expect(isTerminalAutoResponse('\x03')).toBe(false)
      expect(isTerminalAutoResponse('\x04')).toBe(false)
      expect(isTerminalAutoResponse('\x1a')).toBe(false)
    })
  })
})
