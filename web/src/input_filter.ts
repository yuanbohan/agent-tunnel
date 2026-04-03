// Cursor Position Report: \x1b[<digits>;<digits>R
const CPR = /^\x1b\[\d+;\d+R$/

// Extended Cursor Position Report (DECXCPR): \x1b[?<digits>;<digits>R
const DECXCPR = /^\x1b\[\?\d+;\d+R$/

// Primary Device Attributes (DA1): \x1b[?<digits>;<digits>;...c
const DA1 = /^\x1b\[\?[\d;]+c$/

// Secondary Device Attributes (DA2): \x1b[><digits>;<digits>;...c
const DA2 = /^\x1b\[>[\d;]+c$/

// OSC color query response: \x1b]1<0|1>;rgb:...<ST>
// ST is either \x1b\\ (ESC backslash) or \x07 (BEL)
const OSC_COLOR = /^\x1b\]1[01];rgb:[0-9a-fA-F/]+(?:\x1b\\|\x07)$/

/**
 * Returns true if the string is a terminal auto-response that should NOT
 * be forwarded to the PTY. xterm.js generates these responses automatically
 * when it receives query escape sequences in the output stream.
 */
export function isTerminalAutoResponse(data: string): boolean {
  return (
    CPR.test(data) ||
    DECXCPR.test(data) ||
    DA1.test(data) ||
    DA2.test(data) ||
    OSC_COLOR.test(data)
  )
}
