package asn

import "testing"

func TestOrgNameStripsASPrefix(t *testing.T) {
	cases := map[string]string{
		"AS15169 GOOGLE":         "GOOGLE",
		"as123 MCI":              "MCI",
		"MCI Communication":      "MCI Communication",
		"AS44244":                "",
		"":                       "",
		"  AS58224  Iran Cell  ": "Iran Cell",
	}
	for in, want := range cases {
		if got := OrgName(in); got != want {
			t.Errorf("OrgName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLogoKey(t *testing.T) {
	cases := map[string]string{
		"Tose'h Fanavari Ertebabat Pasargad Arian Co. PJS":      "zitel",
		"AS58224 Iran Cell Service and Communication Company":   "irancell",
		"Mobin Net Communication Company (Private Joint Stock)": "mobin-net",
		"Mobile Communication Company of Iran PLC":              "mci",
		"MCI":     "mci",
		"Respina": "respina",
		"GOOGLE":  "",
		"":        "",
	}
	for in, want := range cases {
		if got := LogoKey(in); got != want {
			t.Errorf("LogoKey(%q)=%q want %q", in, got, want)
		}
	}
}
