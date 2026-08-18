package main

import (
	"net/http"
	"testing"
)

// The account-less version, offered from the campaign a volunteer came for.
//
// What matters is that the link CARRIES the campaign. Offered bare, it drops
// whoever follows it on an empty configuration, with nine fields to retype —
// and a typo in any of them goes out to mayors under the campaign's name.

func TestACampaignOffersTheBrowserVersionCarryingItself(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	// the image's own state: it serves a browser build, and no URL is set
	s.browserDir = t.TempDir()

	_, config := clientOn(t, srv, testSlug+".paraphe.test").
		call(http.MethodGet, "/api/config", nil)
	if config["mode"] != "team" {
		t.Fatalf("the subdomain did not resolve its campaign: %v", config)
	}
	want := "/navigateur/?org=" + testSlug
	if got := config["browser_version_url"]; got != want {
		t.Errorf("browser_version_url = %v, want %q", got, want)
	}
}

// An instance serving no browser build and naming no URL offers nothing —
// and says so by an empty string, which is what the interface reads to leave
// the link out rather than render an <a> that goes nowhere.
func TestAnInstanceWithNoBrowserVersionOffersNoLink(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	if s.browserDir != "" {
		t.Fatal("fixture assumption broken: browserDir set")
	}

	_, config := clientOn(t, srv, testSlug+".paraphe.test").
		call(http.MethodGet, "/api/config", nil)
	if got := config["browser_version_url"]; got != "" {
		t.Errorf("browser_version_url = %v with nothing to offer", got)
	}
}

// A single-campaign instance has no subdomain space, so the pre-fill has
// nowhere to resolve `<slug>.<domain>`. The link is still offered — working
// alone in a browser is worth offering — but WITHOUT a parameter that would
// silently do nothing.
func TestASingleCampaignOffersTheLinkWithoutAPreFill(t *testing.T) {
	s, srv := testServer(t)
	s.browserDir = t.TempDir()

	_, config := newClient(t, srv).call(http.MethodGet, "/api/config", nil)
	if got := config["browser_version_url"]; got != "/navigateur/" {
		t.Errorf("browser_version_url = %v on a single-campaign instance: "+
			"an ?org= here resolves against no domain and pre-fills nothing", got)
	}
}

// The apex keeps the plain link: it serves no campaign, so there is none to
// name. Its own screen says what the version is for, in its own words.
func TestTheApexOffersTheBrowserVersionWithoutACampaign(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	s.browserDir = t.TempDir()

	_, config := clientOn(t, srv, "paraphe.test").
		call(http.MethodGet, "/api/config", nil)
	if config["mode"] != "instance" {
		t.Fatalf("the apex resolved a campaign: %v", config)
	}
	if got := config["browser_version_url"]; got != "/navigateur/" {
		t.Errorf("browser_version_url = %v on the apex", got)
	}
}

// A configured URL replaces the self-hosted build, and takes the campaign
// with it: an operator publishing the browser version elsewhere (Pages) is
// pointing at the same application.
func TestAConfiguredBrowserVersionURLAlsoCarriesTheCampaign(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	t.Setenv("PARAPHE_BROWSER_VERSION_URL", "https://exemple.github.io/paraphe/")
	_, srv := testServer(t)

	_, config := clientOn(t, srv, testSlug+".paraphe.test").
		call(http.MethodGet, "/api/config", nil)
	want := "https://exemple.github.io/paraphe/?org=" + testSlug
	if got := config["browser_version_url"]; got != want {
		t.Errorf("browser_version_url = %v, want %q", got, want)
	}
}

// A query the operator wrote SURVIVES: the parameter is set on the URL, not
// appended to the string. Concatenated, `…/?utm=x` became `…/?utm=x?org=y`,
// which is one parameter named "utm" and no campaign at all.
func TestTheCampaignJoinsAQueryTheOperatorAlreadyWrote(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	got := browserVersionFor("https://exemple.test/app/?theme=sombre", "camille")
	want := "https://exemple.test/app/?org=camille&theme=sombre"
	if got != want {
		t.Errorf("browserVersionFor = %q, want %q", got, want)
	}
}

// The setting becomes an href on the home page and on every campaign's
// sign-in screen, and the interface DROPS what is not http(s) rather than
// render it. A wrong value would then be indistinguishable from "this
// instance offers no browser version" — so it is refused where the operator
// who wrote it is looking.
func TestABrowserVersionURLThatIsNotOneIsRefusedAtStartup(t *testing.T) {
	for _, poison := range []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"exemple.test/paraphe",        // relative: resolves against the screen
		"navigateur/",                 // same
		"https://",                    // names no host
		"http://exemple.test/\x00nul", // not a URL at all
	} {
		if err := validBrowserVersionURL(poison); err == nil {
			t.Errorf("validBrowserVersionURL(%q) accepted it", poison)
		}
	}
	// …and the honest values pass, or the guard above would be one that
	// refuses everything.
	for _, good := range []string{
		"/navigateur/",
		"https://exemple.github.io/paraphe/",
		"http://127.0.0.1:5180/",
	} {
		if err := validBrowserVersionURL(good); err != nil {
			t.Errorf("validBrowserVersionURL(%q) refused a legitimate value: %v",
				good, err)
		}
	}
}
