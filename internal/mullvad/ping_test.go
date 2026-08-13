package mullvad

import "testing"

func TestParsePingStatsLinux(t *testing.T) {
	raw := `PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.
64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=12.3 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=13.1 ms

--- 1.1.1.1 ping statistics ---
4 packets transmitted, 4 received, 0% packet loss, time 3005ms
rtt min/avg/max/mdev = 12.345/13.456/14.567/0.890 ms
`
	loss, avg := parsePingStats(raw)
	if loss != 0 {
		t.Fatalf("loss=%v", loss)
	}
	if avg != 13.456 {
		t.Fatalf("avg=%v", avg)
	}
}

func TestParsePingStatsBusybox(t *testing.T) {
	raw := `--- 8.8.8.8 ping statistics ---
3 packets transmitted, 2 packets received, 33% packet loss
round-trip min/avg/max = 10.1/20.5/30.2 ms
`
	loss, avg := parsePingStats(raw)
	if loss != 33 {
		t.Fatalf("loss=%v", loss)
	}
	if avg != 20.5 {
		t.Fatalf("avg=%v", avg)
	}
}

func TestValidPingTarget(t *testing.T) {
	if !validPingTarget("1.1.1.1") || !validPingTarget("one.one.one.one") {
		t.Fatal("expected valid targets")
	}
	if validPingTarget("1.1.1.1; rm -rf /") || validPingTarget("host && id") {
		t.Fatal("expected invalid targets")
	}
}
