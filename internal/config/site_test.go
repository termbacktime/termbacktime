package config

import "testing"

func TestSiteURLOverridesEmbeddedReleaseDefault(t *testing.T) {
	previous := EmbeddedSiteURL
	defer func() { EmbeddedSiteURL = previous }()
	EmbeddedSiteURL = "https://release.example"
	for _, key := range []string{"TERMBACKTIME_SITE_URL", "SITE_URL"} {
		t.Setenv(key, "")
	}
	if SiteURL() != EmbeddedSiteURL {
		t.Fatal("release URL was lost")
	}
	t.Setenv("SITE_URL", "https://temporary.example/")
	if SiteURL() != "https://temporary.example" {
		t.Fatal("runtime URL did not override release URL")
	}
	for _, value := range []string{"", "http://remote.example", "https://name:secret@example.org", "https://example.org/path"} {
		if _, err := ValidateSiteURL(value); err == nil {
			t.Fatalf("accepted invalid website origin %q", value)
		}
	}
	if _, err := ValidateSiteURL("http://localhost:8787"); err != nil {
		t.Fatal(err)
	}
}
