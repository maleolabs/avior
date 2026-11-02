package runtime

import (
	"fmt"
	"strings"

	"github.com/maleolabs/avior/internal/proxy"
	"github.com/maleolabs/avior/internal/state"
)

// SlugDomain menghasilkan domain localhost-only dari nama proyek:
// lowercase, non-alphanumeric → "-", trim "-" di ujung. Fallback "app".
//
//	SlugDomain("feather") = "feather.localhost"
//	SlugDomain("My App!") = "my-app.localhost"
func SlugDomain(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' || r == '.' {
			b.WriteRune('-')
		} else {
			b.WriteRune('-')
		}
	}
	slug = strings.Trim(b.String(), "-")
	for slug == "" {
		slug = "app"
	}
	return slug + ".localhost"
}

// UniqueDomain mengembalikan domain yang tidak dipakai proyek lain. Domain
// default adalah slug(nama proyek); bila sudah dipakai, suffix token proyek
// ditambahkan (feather-a1b2.localhost). selfToken dikecualikan dari cek.
func UniqueDomain(name, selfToken string) (string, error) {
	domain := SlugDomain(name)
	if !proxy.ValidLocalDomain(domain) {
		return "", fmt.Errorf("domain hasil slug invalid: %s", domain)
	}
	used, err := usedDomains(selfToken)
	if err != nil {
		return "", err
	}
	if !used[domain] {
		return domain, nil
	}
	// Collision: tambahkan 4 hex pertama token proyek.
	suffix := selfToken
	if len(suffix) > 4 {
		suffix = suffix[:4]
	}
	if suffix == "" {
		suffix = "x"
	}
	prefix := strings.TrimSuffix(domain, ".localhost")
	candidate := fmt.Sprintf("%s-%s.localhost", prefix, suffix)
	if !proxy.ValidLocalDomain(candidate) {
		return "", fmt.Errorf("domain kandidat collision invalid: %s", candidate)
	}
	if used[candidate] {
		return "", fmt.Errorf("domain %s dipakai proyek lain; gunakan --domain untuk memilih domain lain", candidate)
	}
	return candidate, nil
}

// usedDomains mengembalikan kumpulan domain proyek lain dari index.
func usedDomains(selfToken string) (map[string]bool, error) {
	used := map[string]bool{}
	idx, exists, err := state.LoadIndex()
	if err != nil {
		return nil, err
	}
	if !exists {
		return used, nil
	}
	for _, p := range idx.Projects {
		if p.Token != selfToken && p.Domain != "" {
			used[p.Domain] = true
		}
	}
	return used, nil
}
