package creator

import (
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func TestDeriveRPID(t *testing.T) {
	ok := []struct{ origin, want string }{
		{"https://admin.example.com", "admin.example.com"},
		{"https://admin.example.com:8443", "admin.example.com"},
		{"https://example.com", "example.com"},
		{"http://localhost:8085", "localhost"},
		{"http://app.localhost:8085", "app.localhost"},
	}
	for _, tc := range ok {
		got, err := deriveRPID(tc.origin)
		if err != nil {
			t.Errorf("deriveRPID(%q) error: %v", tc.origin, err)
			continue
		}
		if got != tc.want {
			t.Errorf("deriveRPID(%q) = %q, want %q", tc.origin, got, tc.want)
		}
	}

	bad := []string{
		"",                         // empty
		"admin.example.com",        // no scheme
		"http://admin.example.com", // http on a non-localhost host
		"https://",                 // no host
		"ftp://example.com",        // wrong scheme
	}
	for _, origin := range bad {
		if _, err := deriveRPID(origin); err == nil {
			t.Errorf("deriveRPID(%q) should have errored", origin)
		}
	}
}

func TestNewWithAppStoreWiresAuth(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app, err := appstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	c, err := New(st, Config{AppStore: app, AdminOrigin: "https://admin.example.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !c.authEnabled() {
		t.Fatal("authEnabled() = false, want true when AppStore is set")
	}
	if c.flow == nil {
		t.Fatal("auth flow not initialised")
	}
	if v := c.flow.Verifier(); v.RPID != "admin.example.com" || v.Origin != "https://admin.example.com" {
		t.Errorf("verifier = %+v, want RPID=admin.example.com origin=https://admin.example.com", v)
	}
}

func TestNewAppStoreRequiresValidOrigin(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	app, _ := appstore.Open(":memory:")
	defer app.Close()

	if _, err := New(st, Config{AppStore: app, AdminOrigin: ""}); err == nil {
		t.Error("New with AppStore but empty AdminOrigin should error")
	}
	if _, err := New(st, Config{AppStore: app, AdminOrigin: "http://admin.example.com"}); err == nil {
		t.Error("New with non-localhost http origin should error")
	}
}

func TestNewWithoutAppStoreLeavesAuthOff(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	c, err := New(st, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.authEnabled() {
		t.Error("authEnabled() = true without an AppStore")
	}
}
