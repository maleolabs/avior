package env

import (
	"strings"
	"testing"

	"github.com/maleolabs/avior/internal/mode"
)

func TestValidateModeRequirements(t *testing.T) {
	base := map[string]string{
		"APP_KEY":       "base64:x",
		"APP_ENV":       "production",
		"APP_DEBUG":     "false",
		"DB_CONNECTION": "sqlite",
	}
	if err := Validate(base, mode.ModeProduction); err != nil {
		t.Fatalf("valid production/sqlite: %v", err)
	}
	if err := Validate(base, mode.ModeDevelopment); err != nil {
		t.Fatalf("valid development: %v", err)
	}

	missing := map[string]string{"APP_ENV": "production"}
	err := Validate(missing, mode.ModeProduction)
	if err == nil || !strings.Contains(err.Error(), "APP_KEY wajib diisi") {
		t.Fatalf("missing required: %v", err)
	}

	local := clone(base)
	local["APP_ENV"] = "local"
	if err := Validate(local, mode.ModeProduction); err == nil || err.Error() != "APP_ENV harus production" {
		t.Fatalf("APP_ENV=local di production: %v", err)
	}

	debug := clone(base)
	debug["APP_DEBUG"] = "TRUE"
	if err := Validate(debug, mode.ModeProduction); err == nil || err.Error() != "APP_DEBUG harus false di production" {
		t.Fatalf("APP_DEBUG=true di production: %v", err)
	}

	mysql := clone(base)
	mysql["DB_CONNECTION"] = "mysql"
	if err := Validate(mysql, mode.ModeProduction); err == nil || !strings.Contains(err.Error(), "DB_HOST wajib diisi") {
		t.Fatalf("DB non-sqlite tanpa host: %v", err)
	}
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
