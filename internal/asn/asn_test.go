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
		"error":                  "",
		"Error":                  "",
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
		"Mobile Telecommunication Company of Iran":              "mci",
		"MCI":              "mci",
		"Respina":          "respina",
		"Aria Shatel PJSC": "shatel",
		"AS44285 Shatel":   "shatel",
		"GOOGLE":           "",
		"":                 "",
	}
	for in, want := range cases {
		if got := LogoKey(in); got != want {
			t.Errorf("LogoKey(%q)=%q want %q", in, got, want)
		}
	}
}

func TestWithLogoErrorSentinel(t *testing.T) {
	name, logo := WithLogo("error")
	if name != "ISP lookup failed" || logo != "" {
		t.Fatalf("got %q %q", name, logo)
	}
}
