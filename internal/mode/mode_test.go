package mode

import "testing"

func TestParseMode(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr string
	}{
		{in: "development", want: ModeDevelopment},
		{in: "production", want: ModeProduction},
		{in: "", wantErr: "mode harus development atau production"},
		{in: "Development", wantErr: "mode harus development atau production"},
		{in: "PRODUCTION", wantErr: "mode harus development atau production"},
		{in: "dev", wantErr: "mode harus development atau production"},
		{in: "development ", wantErr: "mode harus development atau production"},
	}
	for _, tt := range tests {
		got, err := ParseMode(tt.in)
		if tt.wantErr != "" {
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("ParseMode(%q) err = %v, want %q", tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseMode(%q) = (%v, %v), want (%v, nil)", tt.in, got, err, tt.want)
		}
	}
}
