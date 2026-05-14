package config

import "testing"

func TestNormalizeSTUNListenAddr(t *testing.T) {
	for _, value := range []string{"off", " disabled ", "NONE", "False"} {
		if got := NormalizeSTUNListenAddr(value); got != "" {
			t.Fatalf("NormalizeSTUNListenAddr(%q) = %q, want empty disabled value", value, got)
		}
	}

	if got := NormalizeSTUNListenAddr(" 127.0.0.1:3478 "); got != "127.0.0.1:3478" {
		t.Fatalf("NormalizeSTUNListenAddr returned %q, want trimmed listen address", got)
	}
}
