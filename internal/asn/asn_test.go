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
