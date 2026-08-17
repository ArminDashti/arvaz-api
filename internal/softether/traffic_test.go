package softether

import "testing"

func TestParseTrafficPrefersDataSizeOverPacketCounts(t *testing.T) {
	block := `
Session Name                                    | SID-alice-1
Outgoing Unicast Packets                        | 100
Incoming Unicast Packets                        | 80
Outgoing Data Size                              | 1,000,000 bytes
Incoming Data Size                              | 250,000 bytes
Outgoing Unicast Total Size                     | 9 bytes
Incoming Unicast Total Size                     | 9 bytes
`
	dl, ul := parseTraffic(block)
	if dl != 250_000 || ul != 1_000_000 {
		t.Fatalf("got download=%d upload=%d", dl, ul)
	}
}

func TestParseTrafficUnicastPlusBroadcast(t *testing.T) {
	block := `
Outgoing Unicast Packets                        | 10
Outgoing Unicast Total Size                     | 1,000 bytes
Incoming Unicast Packets                        | 20
Incoming Unicast Total Size                     | 500 bytes
Outgoing Broadcast Packets                      | 2
Outgoing Broadcast Total Size                   | 100 bytes
Incoming Broadcast Packets                      | 1
Incoming Broadcast Total Size                   | 50 bytes
`
	dl, ul := parseTraffic(block)
	if dl != 550 || ul != 1_100 {
		t.Fatalf("got download=%d upload=%d", dl, ul)
	}
}

func TestParseTrafficIgnoresPacketCountsAlone(t *testing.T) {
	block := `
Outgoing Unicast Packets                        | 99999
Incoming Unicast Packets                        | 88888
Outgoing Broadcast Packets                      | 7
Incoming Broadcast Packets                      | 3
`
	dl, ul := parseTraffic(block)
	if dl != 0 || ul != 0 {
		t.Fatalf("packet counts must not be treated as bytes, got download=%d upload=%d", dl, ul)
	}
}

func TestParseTrafficTransferBytesFallback(t *testing.T) {
	block := `
Session Name                                    | SID-bob-1
Transfer Bytes                                  | 42,000
`
	dl, ul := parseTraffic(block)
	if dl != 42_000 || ul != 0 {
		t.Fatalf("got download=%d upload=%d", dl, ul)
	}
}

func TestEnrichUserFromUserGet(t *testing.T) {
	u := HubUser{Username: "alice"}
	enrichUserFromUserGet(&u, `
Outgoing Unicast Total Size                     | 2,000 bytes
Incoming Unicast Total Size                     | 800 bytes
Outgoing Broadcast Total Size                   | 200 bytes
Incoming Broadcast Total Size                   | 50 bytes
Number of Logins                                | 12
`)
	if u.DownloadBytes != 850 || u.UploadBytes != 2_200 {
		t.Fatalf("got download=%d upload=%d", u.DownloadBytes, u.UploadBytes)
	}
	if u.NumLogins != 12 {
		t.Fatalf("got numLogins=%d", u.NumLogins)
	}
}

func TestParseTrafficUnitSuffixes(t *testing.T) {
	block := `
Outgoing Data Size                              | 1.23 GBytes
Incoming Data Size                              | 450.5 MBytes
`
	dl, ul := parseTraffic(block)
	if dl != 450_500_000 || ul != 1_230_000_000 {
		t.Fatalf("got download=%d upload=%d", dl, ul)
	}
}

func TestParseSizeValueCommaBytes(t *testing.T) {
	if n := parseSizeValue("1,000,000 bytes"); n != 1_000_000 {
		t.Fatalf("got %d", n)
	}
}
