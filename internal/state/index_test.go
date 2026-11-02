package state

import (
	"testing"
)

// TestProjectIndexRoundTrip: upsert → load → update → remove.
func TestProjectIndexRoundTrip(t *testing.T) {
	isolateHome(t)
	entry := ProjectEntry{
		Token:  "abc123def456",
		Root:   "/home/dev/app",
		Name:   "app",
		Domain: "app.localhost",
		Ports:  PortSet{App: 8012, Caddy: 8090, CaddyAdmin: 2027},
	}
	if err := UpsertProject(entry); err != nil {
		t.Fatal(err)
	}
	idx, exists, err := LoadIndex()
	if err != nil || !exists {
		t.Fatalf("LoadIndex: exists=%v err=%v", exists, err)
	}
	if len(idx.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(idx.Projects))
	}
	if idx.Projects[0].Token != entry.Token || idx.Projects[0].Name != "app" {
		t.Fatalf("entry beda: %+v", idx.Projects[0])
	}
	// Update: name berubah, ports tambah vite; entry tetap satu.
	entry.Name = "app-rename"
	entry.Ports = PortSet{App: 8012, Vite: 5189, Caddy: 8090, CaddyAdmin: 2027}
	if err := UpsertProject(entry); err != nil {
		t.Fatal(err)
	}
	idx, _, err = LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Projects) != 1 {
		t.Fatalf("update menambah entry: %d", len(idx.Projects))
	}
	if idx.Projects[0].Name != "app-rename" || idx.Projects[0].Ports.Vite != 5189 {
		t.Fatalf("update tidak diterapkan: %+v", idx.Projects[0])
	}
	// Remove.
	if err := RemoveProject(entry.Token); err != nil {
		t.Fatal(err)
	}
	idx, _, err = LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Projects) != 0 {
		t.Fatalf("setelah remove: %d entry", len(idx.Projects))
	}
}

// TestFindProjectSelector: cari by root, token, name.
func TestFindProjectSelector(t *testing.T) {
	isolateHome(t)
	entry := ProjectEntry{
		Token:  "aabbccddeeff",
		Root:   "/home/dev/shop",
		Name:   "shop",
		Domain: "shop.localhost",
	}
	if err := UpsertProject(entry); err != nil {
		t.Fatal(err)
	}
	for _, sel := range []string{entry.Root, entry.Token, entry.Name} {
		got, found, err := FindProject(sel)
		if err != nil || !found {
			t.Fatalf("FindProject(%q): found=%v err=%v", sel, found, err)
		}
		if got.Token != entry.Token {
			t.Fatalf("FindProject(%q) → token %q, want %q", sel, got.Token, entry.Token)
		}
	}
	_, found, _ := FindProject("tidak-ada")
	if found {
		t.Fatal("FindProject harus false untuk selector tak dikenal")
	}
}
