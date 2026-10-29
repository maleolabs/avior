package runtime

import (
	"reflect"
	"testing"

	"github.com/maleolabs/avior/internal/mode"
)

// servePolicyCase memodelkan default flag production-oriented CLI.
type servePolicyCase struct {
	name           string
	mode           mode.Mode
	changed        map[string]bool
	nonInteractive bool
	virtual        bool // nilai flag terkini (default true = production)
	https          bool
	trust          bool
	want           [3]bool // virtual, https, trust
}

func TestNormalizeServeOptions(t *testing.T) {
	changed := func(keys ...string) map[string]bool {
		m := map[string]bool{}
		for _, k := range keys {
			m[k] = true
		}
		return m
	}
	tests := []servePolicyCase{
		{
			name: "production mempertahankan default deploy-like",
			mode: mode.ModeProduction, changed: map[string]bool{},
			virtual: true, https: true, trust: true,
			want: [3]bool{true, true, true},
		},
		{
			name: "development menurunkan semua flag default",
			mode: mode.ModeDevelopment, changed: map[string]bool{},
			virtual: true, https: true, trust: true,
			want: [3]bool{false, false, false},
		},
		{
			name: "development dengan --https eksplisit tetap https dan memaksa virtual",
			mode: mode.ModeDevelopment, changed: changed("https"),
			virtual: true, https: true, trust: true,
			want: [3]bool{true, true, false},
		},
		{
			name: "development dengan --trust-local-ca eksplisit tetap trust, https default turun",
			mode: mode.ModeDevelopment, changed: changed("trust-local-ca"),
			virtual: true, https: true, trust: true,
			want: [3]bool{false, false, true},
		},
		{
			name: "non-interactive mematikan trust walau production",
			mode: mode.ModeProduction, changed: map[string]bool{}, nonInteractive: true,
			virtual: true, https: true, trust: true,
			want: [3]bool{true, true, false},
		},
		{
			name: "non-interactive dengan --trust-local-ca eksplisit tetap trust",
			mode: mode.ModeProduction, changed: changed("trust-local-ca"), nonInteractive: true,
			virtual: true, https: true, trust: true,
			want: [3]bool{true, true, true},
		},
		{
			name: "https selalu memaksa virtual (production, --virtual-domain=false eksplisit)",
			mode: mode.ModeProduction, changed: changed("virtual-domain"),
			virtual: false, https: true, trust: true,
			want: [3]bool{true, true, true},
		},
		{
			name: "flag user sudah false tidak dinaikkan di development",
			mode: mode.ModeDevelopment, changed: map[string]bool{},
			virtual: false, https: false, trust: false,
			want: [3]bool{false, false, false},
		},
	}
	for _, tt := range tests {
		v, h, tr := NormalizeServeOptions(tt.mode, tt.changed, tt.nonInteractive, tt.virtual, tt.https, tt.trust)
		got := [3]bool{v, h, tr}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: NormalizeServeOptions = %v, want %v", tt.name, got, tt.want)
		}
	}
}
