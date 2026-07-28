package handler

import "testing"

func TestValidateFontContentAcceptsKnownFontMagic(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		content  []byte
		wantType string
	}{
		{name: "woff2", filename: "brand.woff2", content: []byte("wOF2fontdata"), wantType: "font/woff2"},
		{name: "woff", filename: "brand.woff", content: []byte("wOFFfontdata"), wantType: "font/woff"},
		{name: "otf", filename: "brand.otf", content: []byte("OTTOfontdata"), wantType: "font/otf"},
		{name: "ttf", filename: "brand.ttf", content: []byte{0x00, 0x01, 0x00, 0x00, 'f', 'o', 'n', 't'}, wantType: "font/ttf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateFontContent(tc.filename, tc.content)
			if err != nil {
				t.Fatalf("validateFontContent returned error: %v", err)
			}
			if got != tc.wantType {
				t.Fatalf("mime = %q, want %q", got, tc.wantType)
			}
		})
	}
}

func TestValidateFontContentRejectsMismatchedContent(t *testing.T) {
	if _, err := validateFontContent("brand.woff2", []byte("not a font")); err == nil {
		t.Fatal("expected mismatched font content to be rejected")
	}
}

func TestValidateFontContentRejectsUnsupportedExtension(t *testing.T) {
	if _, err := validateFontContent("brand.exe", []byte("wOF2fontdata")); err == nil {
		t.Fatal("expected unsupported extension to be rejected")
	}
}
