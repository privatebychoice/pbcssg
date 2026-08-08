package creator

import (
	"strings"
	"testing"
)

func TestHeaderBrandSettings(t *testing.T) {
	h := newHarness(t)

	// Text mode with an override + centred alignment persists and applies.
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"headerBrand": "text", "headerAlign": "center", "brandText": "Untracked",
	}))
	if rec.Code != 200 {
		t.Fatalf("save text brand: %d\n%s", rec.Code, rec.Body.String())
	}
	if b := h.c.state().build.Brand(); b.Mode != "text" || b.Align != "center" || b.Text != "Untracked" {
		t.Errorf("text brand not applied: %+v", b)
	}

	// Logo mode with a real Media-library logo saves and applies.
	h.upload("/admin/media", "logo.png", pngBytes(t), true)
	assets, _ := h.st.Assets()
	if len(assets) == 0 {
		t.Fatal("logo upload failed")
	}
	logo := "/media/" + assets[0].SHA256 + ".png"
	ok := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"headerBrand": "logo", "logoSrc": logo, "logoAlt": "TUL logo", "logoHeight": "large",
	}))
	if ok.Code != 200 {
		t.Fatalf("save logo brand: %d\n%s", ok.Code, ok.Body.String())
	}
	if b := h.c.state().build.Brand(); b.Mode != "logo" || b.LogoSrc != logo || b.LogoHeight != "large" {
		t.Errorf("logo brand not applied: %+v", b)
	}

	// Rejections, each with a clear inline message.
	bad := func(fields map[string]string, want string) {
		t.Helper()
		fields["siteName"] = "TUL"
		fields["baseURL"] = "https://tul.example"
		fields["version"] = "1.0"
		rec := h.post("/admin/settings", h.form(fields))
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("expected rejection %q, got code=%d\n%s", want, rec.Code, rec.Body.String())
		}
	}
	bad(map[string]string{"headerBrand": "logo", "logoAlt": "x"}, "Pick a logo image")
	bad(map[string]string{"headerBrand": "logo", "logoSrc": "/media/" + strings.Repeat("a", 64) + ".png", "logoAlt": "x"}, "not in the Media library")
	bad(map[string]string{"headerBrand": "logo", "logoSrc": logo}, "Add alt text")
}

func TestHeaderBrandDarkLogoSettings(t *testing.T) {
	h := newHarness(t)

	// Two distinct Media-library images for the light and dark logos.
	h.upload("/admin/media", "light.png", pngBytes(t), true)
	h.upload("/admin/media", "dark.png", pngBytes2(t), true)
	assets, _ := h.st.Assets()
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(assets))
	}
	light := "/media/" + assets[0].SHA256 + ".png"
	dark := "/media/" + assets[1].SHA256 + ".png"

	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"headerBrand": "logo", "logoSrc": light, "logoSrcDark": dark,
		"logoAlt": "TUL logo", "logoHeight": "medium",
	}))
	if rec.Code != 200 {
		t.Fatalf("save dark logo: %d\n%s", rec.Code, rec.Body.String())
	}
	if b := h.c.state().build.Brand(); b.LogoSrcDark != dark || !b.HasDarkLogo() {
		t.Errorf("dark logo not applied: LogoSrcDark=%q HasDarkLogo=%v", b.LogoSrcDark, b.HasDarkLogo())
	}

	// Invalid dark-logo values are rejected with clear messages (it is optional,
	// but when set it must be a real Media-library path).
	bad := func(fields map[string]string, want string) {
		t.Helper()
		fields["siteName"], fields["baseURL"], fields["version"] = "TUL", "https://tul.example", "1.0"
		fields["headerBrand"], fields["logoSrc"], fields["logoAlt"] = "logo", light, "TUL logo"
		rec := h.post("/admin/settings", h.form(fields))
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("expected rejection %q, got code=%d\n%s", want, rec.Code, rec.Body.String())
		}
	}
	bad(map[string]string{"logoSrcDark": "not-a-media-path"}, "must be a Media-library path")
	bad(map[string]string{"logoSrcDark": "/media/" + strings.Repeat("b", 64) + ".png"}, "dark-mode logo is not in the Media library")
}
