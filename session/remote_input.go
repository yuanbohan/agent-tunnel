package session

import "strings"

func EncodeRemoteTextInput(text string) []byte {
	return []byte(text)
}

func EncodeRemoteSubmitInput(text string) []byte {
	data := append([]byte(nil), text...)
	return append(data, remoteEnterInput()...)
}

func EncodeRemoteKeyInput(key string, ctrl, alt, shift bool) ([]byte, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	if normalized == "" {
		return nil, false
	}

	if ctrl && !alt && len(normalized) == 1 {
		ch := normalized[0]
		if ch >= 'A' && ch <= 'Z' {
			return []byte{ch - 'A' + 1}, true
		}
	}

	if ctrl || alt {
		return nil, false
	}

	switch normalized {
	case "ENTER":
		return remoteEnterInput(), true
	case "BACKSPACE":
		return []byte{0x7f}, true
	case "TAB":
		return []byte{'\t'}, true
	case "ESCAPE":
		return []byte{0x1b}, true
	case "UP":
		return []byte("\x1b[A"), true
	case "DOWN":
		return []byte("\x1b[B"), true
	case "RIGHT":
		return []byte("\x1b[C"), true
	case "LEFT":
		return []byte("\x1b[D"), true
	case "HOME":
		return []byte("\x1b[H"), true
	case "END":
		return []byte("\x1b[F"), true
	case "PAGE_UP":
		return []byte("\x1b[5~"), true
	case "PAGE_DOWN":
		return []byte("\x1b[6~"), true
	case "DELETE":
		return []byte("\x1b[3~"), true
	default:
		_ = shift
		return nil, false
	}
}

func remoteEnterInput() []byte {
	return []byte{'\r'}
}
