package pairingqr

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/term"
)

const CompactPrefix = "TP2:"
const CompactSuffix = "."
const base45Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

const (
	compactIDBytes        = 12
	compactNonceBytes     = 16
	compactPublicKeyBytes = 32
	compactSignatureBytes = 64
)

const terminalQuietZoneModules = 2

const (
	qrDarkForeground  = "\x1b[30m"
	qrLightForeground = "\x1b[37m"
	qrDarkBackground  = "\x1b[40m"
	qrLightBackground = "\x1b[47m"
	qrBackgroundReset = "\x1b[0m"
)

type TerminalQRCode struct {
	Output  string
	Columns int
	Rows    int
}

type TerminalSize struct {
	Columns int
	Rows    int
}

type CompactInvitation struct {
	Version         int
	InvitationID    string
	CorrelationID   string
	Nonce           string
	DeviceID        string
	DisplayName     string
	DaemonPublicKey string
	ExpiresAt       int64
	Signature       string
}

func CompactPayload(invitation CompactInvitation) (string, error) {
	if invitation.Version <= 0 || invitation.Version > math.MaxUint8 {
		return "", fmt.Errorf("invalid pairing version %d", invitation.Version)
	}
	deviceID, err := decodeOpaqueHexID("computer_id", invitation.DeviceID, "dev")
	if err != nil {
		return "", err
	}
	invitationID, err := decodeOpaqueHexID("invitation_id", invitation.InvitationID, "pair")
	if err != nil {
		return "", err
	}
	correlationID, err := decodeOpaqueHexID("correlation_id", invitation.CorrelationID, "corr")
	if err != nil {
		return "", err
	}
	nonce, err := decodeFixedHex("nonce", invitation.Nonce, compactNonceBytes)
	if err != nil {
		return "", err
	}
	publicKey, err := decodeFixedHex("computer_public_key", invitation.DaemonPublicKey, compactPublicKeyBytes)
	if err != nil {
		return "", err
	}
	signature, err := decodeFixedHex("signature", invitation.Signature, compactSignatureBytes)
	if err != nil {
		return "", err
	}
	if invitation.ExpiresAt <= 0 || invitation.ExpiresAt > math.MaxUint32 {
		return "", fmt.Errorf("expires_at out of compact range")
	}
	displayName := []byte(strings.TrimSpace(invitation.DisplayName))
	if len(displayName) > math.MaxUint8 {
		return "", fmt.Errorf("computer_display_name too long for compact qr")
	}

	payload := make([]byte, 0, 1+compactIDBytes*3+compactNonceBytes+4+compactPublicKeyBytes+compactSignatureBytes+1+len(displayName))
	payload = append(payload, byte(invitation.Version))
	payload = append(payload, deviceID...)
	payload = append(payload, invitationID...)
	payload = append(payload, correlationID...)
	payload = append(payload, nonce...)
	payload = binary.BigEndian.AppendUint32(payload, uint32(invitation.ExpiresAt))
	payload = append(payload, publicKey...)
	payload = append(payload, signature...)
	payload = append(payload, byte(len(displayName)))
	payload = append(payload, displayName...)
	return CompactPrefix + base45Encode(payload) + CompactSuffix, nil
}

func RenderTerminal(payload string) (TerminalQRCode, error) {
	code, err := qrcode.New(payload, qrcode.Low)
	if err != nil {
		return TerminalQRCode{}, err
	}
	code.DisableBorder = true
	bitmap := withQuietZone(code.Bitmap(), terminalQuietZoneModules)
	if len(bitmap) == 0 {
		return TerminalQRCode{}, errors.New("qr bitmap is empty")
	}
	var out strings.Builder
	columns := 0
	for y := 0; y < len(bitmap); y += 2 {
		top := bitmap[y]
		if len(top) > columns {
			columns = len(top)
		}
		var bottom []bool
		if y+1 < len(bitmap) {
			bottom = bitmap[y+1]
		}
		currentStyle := ""
		for x, topDark := range top {
			bottomDark := false
			if bottom != nil && x < len(bottom) {
				bottomDark = bottom[x]
			}
			style := qrModuleForeground(topDark) + qrModuleBackground(bottomDark)
			if style != currentStyle {
				out.WriteString(style)
				currentStyle = style
			}
			out.WriteString("▀")
		}
		if currentStyle != "" {
			out.WriteString(qrBackgroundReset)
		}
		out.WriteByte('\n')
	}
	return TerminalQRCode{
		Output:  out.String(),
		Columns: columns,
		Rows:    (len(bitmap) + 1) / 2,
	}, nil
}

func withQuietZone(bitmap [][]bool, quietZone int) [][]bool {
	if quietZone <= 0 || len(bitmap) == 0 {
		return bitmap
	}
	width := 0
	for _, row := range bitmap {
		if len(row) > width {
			width = len(row)
		}
	}
	if width == 0 {
		return bitmap
	}
	paddedWidth := width + quietZone*2
	out := make([][]bool, 0, len(bitmap)+quietZone*2)
	for range quietZone {
		out = append(out, make([]bool, paddedWidth))
	}
	for _, row := range bitmap {
		padded := make([]bool, paddedWidth)
		copy(padded[quietZone:], row)
		out = append(out, padded)
	}
	for range quietZone {
		out = append(out, make([]bool, paddedWidth))
	}
	return out
}

func SizeWarning(qr TerminalQRCode, size TerminalSize, promptRows int) string {
	requiredRows := qr.Rows + promptRows
	if qr.Columns <= size.Columns && requiredRows <= size.Rows {
		return ""
	}
	return fmt.Sprintf(
		"QR size: %dx%d terminal cells. Current terminal: %dx%d. Enlarge the window or run `tunnel pair --json` and paste it in the app.",
		qr.Columns,
		qr.Rows,
		size.Columns,
		size.Rows,
	)
}

func TerminalSizeForWriter(stdout io.Writer) (TerminalSize, bool) {
	outFile, ok := stdout.(*os.File)
	if !ok {
		return TerminalSize{}, false
	}
	columns, rows, err := term.GetSize(int(outFile.Fd()))
	if err != nil || columns <= 0 || rows <= 0 {
		return TerminalSize{}, false
	}
	return TerminalSize{Columns: columns, Rows: rows}, true
}

func decodeOpaqueHexID(field, value, prefix string) ([]byte, error) {
	normalized := strings.TrimSpace(value)
	raw, ok := strings.CutPrefix(normalized, prefix+"_")
	if !ok {
		return nil, fmt.Errorf("%s must start with %s_", field, prefix)
	}
	decoded, err := decodeFixedHex(field, raw, compactIDBytes)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeFixedHex(field, value string, wantBytes int) ([]byte, error) {
	normalized := strings.TrimSpace(value)
	if len(normalized) != wantBytes*2 {
		return nil, fmt.Errorf("%s must be %d hex bytes", field, wantBytes)
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex", field)
	}
	return decoded, nil
}

func base45Encode(data []byte) string {
	var out strings.Builder
	out.Grow(len(data)*3/2 + 2)
	for i := 0; i < len(data); {
		if i+1 < len(data) {
			value := int(data[i])<<8 | int(data[i+1])
			e := value / (45 * 45)
			value %= 45 * 45
			d := value / 45
			c := value % 45
			out.WriteByte(base45Alphabet[c])
			out.WriteByte(base45Alphabet[d])
			out.WriteByte(base45Alphabet[e])
			i += 2
			continue
		}
		value := int(data[i])
		d := value / 45
		c := value % 45
		out.WriteByte(base45Alphabet[c])
		out.WriteByte(base45Alphabet[d])
		i++
	}
	return out.String()
}

func qrModuleForeground(dark bool) string {
	if dark {
		return qrDarkForeground
	}
	return qrLightForeground
}

func qrModuleBackground(dark bool) string {
	if dark {
		return qrDarkBackground
	}
	return qrLightBackground
}
