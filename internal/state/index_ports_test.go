package state

import (
	"testing"
)

// TestUsedPortsExcludesSelf: port proyek sendiri tidak dianggap dipakai.
// TestUsedPortsCollision: port proyek lain terdeteksi.
func TestUsedPortsExcludesSelf(t *testing.T) {
	isolateHome(t)
	if err := UpsertProject(ProjectEntry{
		Token: "self",
		Root:  "/home/dev/app",
		Name:  "app",
		Ports: PortSet{App: 8001, Vite: 5174, Caddy: 8081, CaddyAdmin: 2020},
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertProject(ProjectEntry{
		Token: "other",
		Root:  "/home/dev/shop",
		Name:  "shop",
		Ports: PortSet{App: 8002, Vite: 5175, Caddy: 8082, CaddyAdmin: 2021},
	}); err != nil {
		t.Fatal(err)
	}
	self := UsedPorts("self")
	for _, p := range []int{8001, 5174, 8081, 2020} {
		if self[p] {
			t.Fatalf("port sendiri %d tidak boleh terdeteksi", p)
		}
	}
	for _, p := range []int{8002, 5175, 8082, 2021} {
		if !self[p] {
			t.Fatalf("port proyek lain %d harus terdeteksi", p)
		}
	}
}
