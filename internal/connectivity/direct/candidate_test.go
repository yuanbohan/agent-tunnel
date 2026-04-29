package direct

import (
	"net"
	"reflect"
	"testing"
)

func TestSanitizePrivateUDPAddrsKeepsOnlyAllowedRanges(t *testing.T) {
	got := SanitizePrivateUDPAddrs([]*net.UDPAddr{
		{IP: net.IPv4(10, 0, 0, 2), Port: 5000},
		{IP: net.IPv4(172, 16, 0, 2), Port: 5000},
		{IP: net.IPv4(192, 168, 1, 3), Port: 5000},
		{IP: net.IPv4(169, 254, 1, 3), Port: 5000},
		{IP: net.ParseIP("fd00::1"), Port: 5000},
		{IP: net.ParseIP("fe80::1"), Port: 5000},
		{IP: net.IPv4(8, 8, 8, 8), Port: 5000},
		{IP: net.IPv4(127, 0, 0, 1), Port: 5000},
		{IP: net.IPv4(192, 0, 2, 10), Port: 5000},
	}, PrivateAddressOptions{})

	want := []string{
		"10.0.0.2:5000",
		"169.254.1.3:5000",
		"172.16.0.2:5000",
		"192.168.1.3:5000",
		"[fd00::1]:5000",
		"[fe80::1]:5000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SanitizePrivateUDPAddrs = %#v, want %#v", got, want)
	}
}

func TestSanitizePrivateUDPAddrsCanAllowLoopbackAndTestNetworks(t *testing.T) {
	got := SanitizePrivateUDPAddrs([]*net.UDPAddr{
		{IP: net.IPv4(127, 0, 0, 1), Port: 5000},
		{IP: net.IPv4(192, 0, 2, 10), Port: 5000},
	}, PrivateAddressOptions{AllowLoopback: true, AllowTestNetworks: true})

	want := []string{"127.0.0.1:5000", "192.0.2.10:5000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SanitizePrivateUDPAddrs = %#v, want %#v", got, want)
	}
}

func TestSanitizePrivateUDPAddrsDeduplicatesAndCapsDeterministically(t *testing.T) {
	got := SanitizePrivateUDPAddrs([]*net.UDPAddr{
		{IP: net.IPv4(10, 0, 0, 3), Port: 5000},
		{IP: net.IPv4(10, 0, 0, 1), Port: 5000},
		{IP: net.IPv4(10, 0, 0, 2), Port: 5000},
		{IP: net.IPv4(10, 0, 0, 1), Port: 5000},
	}, PrivateAddressOptions{Limit: 2})

	want := []string{"10.0.0.1:5000", "10.0.0.2:5000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SanitizePrivateUDPAddrs = %#v, want %#v", got, want)
	}
}

func TestNewCandidateSetNormalizesPublicAndPrivateCandidates(t *testing.T) {
	got := NewCandidateSet(
		&net.UDPAddr{IP: net.IPv4(203, 0, 113, 1), Port: 6000},
		[]*net.UDPAddr{{IP: net.IPv4(10, 0, 0, 1), Port: 6000}},
		PrivateAddressOptions{},
	)

	if got.PublicUDPAddr != "203.0.113.1:6000" {
		t.Fatalf("PublicUDPAddr = %q, want 203.0.113.1:6000", got.PublicUDPAddr)
	}
	if !reflect.DeepEqual(got.PrivateUDPAddrs, []string{"10.0.0.1:6000"}) {
		t.Fatalf("PrivateUDPAddrs = %#v, want one private candidate", got.PrivateUDPAddrs)
	}
}
