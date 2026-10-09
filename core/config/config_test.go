package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Servers()) != 0 || s.Settings().Theme != "system" {
		t.Fatalf("unexpected empty config: %+v %+v", s.Servers(), s.Settings())
	}
	srv, err := s.SaveServer(Server{Host: " 1.2.3.4 ", User: "root", Auth: AuthPassword})
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID == "" || srv.Port != 22 || srv.Name != "1.2.3.4" || srv.PortRange != DefaultPortRange {
		t.Fatalf("defaults not applied: %+v", srv)
	}
	srv.Name = "阿里云-1"
	if _, err := s.SaveServer(srv); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Server(srv.ID)
	if !ok || got.Name != "阿里云-1" || len(s2.Servers()) != 1 {
		t.Fatalf("reload: %+v", s2.Servers())
	}
	if err := s2.DeleteServer(srv.ID); err != nil {
		t.Fatal(err)
	}
	if len(s2.Servers()) != 0 {
		t.Fatal("server not deleted")
	}
}

func TestValidate(t *testing.T) {
	cases := []Server{
		{User: "root", Auth: AuthPassword},
		{Host: "h", Auth: AuthPassword},
		{Host: "h", User: "u", Auth: AuthKey},
		{Host: "h", User: "u", Auth: "x"},
		{Host: "h", User: "u", Auth: AuthAgent, Port: 70000},
		{Host: "h", User: "u", Auth: AuthAgent, PortRange: [2]int{2000, 1000}},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

// TestKeyring uses the real system keychain: BERTH_KEYRING_TEST=1 go test.
func TestKeyring(t *testing.T) {
	if os.Getenv("BERTH_KEYRING_TEST") != "1" {
		t.Skip("set BERTH_KEYRING_TEST=1 to use the system keychain")
	}
	k := Keyring{}
	id := "test-" + newID()
	defer k.Delete(id)
	if s, err := k.Get(id); err != nil || s != "" {
		t.Fatalf("missing secret: %q %v", s, err)
	}
	if err := k.Set(id, "p@ss 密码 'x'"); err != nil {
		t.Fatal(err)
	}
	if s, err := k.Get(id); err != nil || s != "p@ss 密码 'x'" {
		t.Fatalf("get = %q %v", s, err)
	}
	if err := k.Set(id, ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := k.Get(id); s != "" {
		t.Fatalf("not deleted: %q", s)
	}
}
