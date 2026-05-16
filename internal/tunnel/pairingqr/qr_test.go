package pairingqr

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	gozxingqrcode "github.com/makiuchi-d/gozxing/qrcode"
)

func TestCompactPayloadBuildsShortPairingPayload(t *testing.T) {
	payload, err := CompactPayload(testCompactInvitation())
	if err != nil {
		t.Fatalf("CompactPayload returned error: %v", err)
	}
	if !strings.HasPrefix(payload, CompactPrefix) {
		t.Fatalf("CompactPayload = %q, want %s prefix", payload, CompactPrefix)
	}
	if !strings.HasSuffix(payload, CompactSuffix) {
		t.Fatalf("CompactPayload = %q, want %s suffix", payload, CompactSuffix)
	}
	if strings.Contains(payload, "pair_") || strings.Contains(payload, "corr_") || strings.Contains(payload, "dev_") {
		t.Fatalf("CompactPayload = %q, should encode opaque ids as raw bytes", payload)
	}
	if len(payload) > 280 {
		t.Fatalf("CompactPayload length = %d, want compact payload", len(payload))
	}
}

func TestRenderTerminalDecodesOriginalPayload(t *testing.T) {
	payload, err := CompactPayload(testCompactInvitation())
	if err != nil {
		t.Fatalf("CompactPayload returned error: %v", err)
	}
	rendered, err := RenderTerminal(payload)
	if err != nil {
		t.Fatalf("RenderTerminal returned error: %v", err)
	}
	image := terminalQRCodeImage(t, rendered.Output, 8)
	bitmap, err := gozxing.NewBinaryBitmapFromImage(image)
	if err != nil {
		t.Fatalf("NewBinaryBitmapFromImage returned error: %v", err)
	}
	result, err := gozxingqrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if result.GetText() != payload {
		t.Fatalf("decoded payload = %q, want original payload", result.GetText())
	}
}

func TestRenderTerminalUsesCompactHalfBlockCells(t *testing.T) {
	payload, err := CompactPayload(testCompactInvitation())
	if err != nil {
		t.Fatalf("CompactPayload returned error: %v", err)
	}
	rendered, err := RenderTerminal(payload)
	if err != nil {
		t.Fatalf("RenderTerminal returned error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(rendered.Output, "\n"), "\n")
	moduleRows := terminalQRCodeRows(t, rendered.Output)
	if !strings.Contains(rendered.Output, "▀") {
		t.Fatalf("rendered QR = %q, want half-block cells", rendered.Output)
	}
	if len(lines)*2 != len(moduleRows) {
		t.Fatalf("rendered lines = %d, module rows = %d, want two module rows per terminal line", len(lines), len(moduleRows))
	}
	if rendered.Columns > 56 || rendered.Rows > 28 {
		t.Fatalf("QR dimensions = %dx%d, want short pairing token bounds", rendered.Columns, rendered.Rows)
	}
	assertQuietZone(t, moduleRows, terminalQuietZoneModules)
}

func TestCompactPayloadRejectsUnexpectedOpaqueID(t *testing.T) {
	_, err := CompactPayload(CompactInvitation{
		Version:         2,
		DeviceID:        "computer-1",
		InvitationID:    "pair_111111111111111111111111",
		CorrelationID:   "corr_222222222222222222222222",
		Nonce:           strings.Repeat("0", 32),
		DaemonPublicKey: strings.Repeat("4", 64),
		ExpiresAt:       1_765_376_720,
		Signature:       strings.Repeat("6", 128),
	})
	if err == nil {
		t.Fatal("CompactPayload returned nil error for non-compact computer id")
	}
}

func TestSizeWarningUsesTerminalDimensions(t *testing.T) {
	warning := SizeWarning(TerminalQRCode{Columns: 93, Rows: 47}, TerminalSize{Columns: 80, Rows: 24}, 2)
	if !strings.Contains(warning, "QR size: 93x47") || !strings.Contains(warning, "Current terminal: 80x24") {
		t.Fatalf("warning = %q, want QR and terminal dimensions", warning)
	}
	if warning := SizeWarning(TerminalQRCode{Columns: 36, Rows: 18}, TerminalSize{Columns: 80, Rows: 24}, 2); warning != "" {
		t.Fatalf("warning = %q, want no warning when QR fits", warning)
	}
}

func testCompactInvitation() CompactInvitation {
	return CompactInvitation{
		Version:         2,
		DeviceID:        "dev_333333333333333333333333",
		InvitationID:    "pair_111111111111111111111111",
		CorrelationID:   "corr_222222222222222222222222",
		Nonce:           "000102030405060708090a0b0c0d0e0f",
		DisplayName:     "yuanbo's MacBook Air",
		DaemonPublicKey: strings.Repeat("4", 64),
		ExpiresAt:       1_765_376_720,
		Signature:       strings.Repeat("6", 128),
	}
}

func terminalQRCodeImage(t *testing.T, rendered string, scale int) image.Image {
	t.Helper()
	rows := terminalQRCodeRows(t, rendered)
	if len(rows) == 0 {
		t.Fatal("rendered QR has no rows")
	}
	modules := len(rows[0])
	img := image.NewGray(image.Rect(0, 0, modules*scale, len(rows)*scale))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetGray(x, y, color.Gray{Y: 0xff})
		}
	}
	for row, rowModules := range rows {
		if len(rowModules) != modules {
			t.Fatalf("line %d width = %d, want %d", row, len(rowModules), modules)
		}
		for col, dark := range rowModules {
			if !dark {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray(col*scale+dx, row*scale+dy, color.Gray{Y: 0x00})
				}
			}
		}
	}
	return img
}

func terminalQRCodeRows(t *testing.T, rendered string) [][]bool {
	t.Helper()
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	var rows [][]bool
	for row, line := range lines {
		top, bottom := terminalQRCodeModulePair(t, line, row)
		rows = append(rows, top, bottom)
	}
	return rows
}

func terminalQRCodeModulePair(t *testing.T, line string, row int) ([]bool, []bool) {
	t.Helper()
	var top []bool
	var bottom []bool
	topDark := false
	bottomDark := false
	for i := 0; i < len(line); {
		switch {
		case strings.HasPrefix(line[i:], qrDarkForeground):
			topDark = true
			i += len(qrDarkForeground)
		case strings.HasPrefix(line[i:], qrLightForeground):
			topDark = false
			i += len(qrLightForeground)
		case strings.HasPrefix(line[i:], qrDarkBackground):
			bottomDark = true
			i += len(qrDarkBackground)
		case strings.HasPrefix(line[i:], qrLightBackground):
			bottomDark = false
			i += len(qrLightBackground)
		case strings.HasPrefix(line[i:], qrBackgroundReset):
			topDark = false
			bottomDark = false
			i += len(qrBackgroundReset)
		case strings.HasPrefix(line[i:], "▀"):
			top = append(top, topDark)
			bottom = append(bottom, bottomDark)
			i += len("▀")
		default:
			t.Fatalf("line %d has unexpected QR token at byte %d: %q", row, i, line[i:])
		}
	}
	return top, bottom
}

func assertQuietZone(t *testing.T, rows [][]bool, quietZone int) {
	t.Helper()
	if len(rows) < quietZone*2 {
		t.Fatalf("QR rows = %d, want at least %d quiet rows", len(rows), quietZone*2)
	}
	width := len(rows[0])
	for rowIndex, row := range rows {
		if len(row) != width {
			t.Fatalf("row %d width = %d, want %d", rowIndex, len(row), width)
		}
		if rowIndex < quietZone || rowIndex >= len(rows)-quietZone {
			assertLightModules(t, row, rowIndex)
			continue
		}
		assertLightModules(t, row[:quietZone], rowIndex)
		assertLightModules(t, row[width-quietZone:], rowIndex)
	}
}

func assertLightModules(t *testing.T, modules []bool, row int) {
	t.Helper()
	for column, dark := range modules {
		if dark {
			t.Fatalf("quiet zone row %d column %d is dark", row, column)
		}
	}
}
