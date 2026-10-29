package detect

import "testing"

func TestConciseDetail(t *testing.T) {
	if got := conciseDetail("\n  tool 1.2\r\nextra output\n"); got != "tool 1.2" {
		t.Fatalf("got %q", got)
	}
}
