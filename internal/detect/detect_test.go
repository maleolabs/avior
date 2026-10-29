package detect

import "testing"

func TestConciseDetail(t *testing.T) {
	if got := conciseDetail("\n  tool 1.2\r\nextra output\n"); got != "tool 1.2" {
		t.Fatalf("got %q", got)
	}
}

func TestSafeBatchArg(t *testing.T) {
	for _, arg := range []string{"--version", "-v", "1.2"} {
		if !safeBatchArg(arg) {
			t.Errorf("safeBatchArg(%q) = false", arg)
		}
	}
	for _, arg := range []string{"", "x&whoami", "x y", "x%PATH%", `x\"`} {
		if safeBatchArg(arg) {
			t.Errorf("safeBatchArg(%q) = true", arg)
		}
	}
}
