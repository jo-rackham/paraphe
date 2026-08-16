package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Routes an anonymous caller is MEANT to reach, and why. Everything else in
// /api must refuse one, and this list is the whole of the exception.
//
// A route added without its guard works — that is what makes the omission
// invisible. This walks the router's own tree, so a route cannot be added
// without being judged here, and calls each one with no session at all: the
// answer, not the middleware chain, is what says whether a wall exists.
var publicAPIRoutes = map[string]string{
	"GET /api/config": "the interface has to know what it is talking to " +
		"before anyone can sign in",
	"POST /api/session":   "signing in",
	"DELETE /api/session": "signing out, which must work even on a dead session",
	"GET /api/campaign/public": "read cross-origin by the browser version: " +
		"the campaign a mayor already reads in every message",
	"POST /api/request": "the public hosting request form, on the apex",
	"POST /api/team/request": "the public form asking a campaign to open a " +
		"local team — answered for a visitor who has no account yet, which " +
		"is the point: it creates nothing until the coordination accepts",
	"GET /api/campaigns": "the public directory of hosted campaigns, on " +
		"the apex — names and addresses every subdomain already tells",
}

func TestEveryAPIRouteRefusesAnAnonymousCaller(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	seedMayors(t, s, 2, "01")

	seen := map[string]bool{}
	walked := 0
	err := chi.Walk(s.router(), func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api") {
			return nil
		}
		walked++
		// chi writes a trailing "/" on a sub-router's index; the concrete
		// path is what a client would send.
		path := strings.TrimSuffix(route, "/")
		if path == "/api" {
			return nil
		}
		// Any value will do: a guard that refuses does so before reading it,
		// and one that does not refuse is the finding.
		for _, param := range []string{"{insee}", "{email}", "{id}"} {
			path = strings.ReplaceAll(path, param, "01000")
		}
		key := method + " " + route
		seen[key] = true
		if _, public := publicAPIRoutes[method+" "+strings.TrimSuffix(route, "/")]; public {
			return nil
		}

		c := clientOn(t, srv, testSlug+".paraphe.test")
		code, _ := c.call(method, path, map[string]any{})
		switch code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return nil
		default:
			t.Errorf("%s %s answers %d to a caller with no session. Either it "+
				"is behind a guard it is not behind, or it belongs in "+
				"publicAPIRoutes with a reason", method, route, code)
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked < 15 {
		t.Fatalf("only %d /api routes walked: the tree was not read, and this "+
			"test would agree with an application that has no guards at all",
			walked)
	}
	// A permission covering nothing is a claim about a route that has moved,
	// and the next route written under that name would inherit it.
	for key, why := range publicAPIRoutes {
		if !seen[key] && !seen[key+"/"] {
			t.Errorf("%s is listed as public (%s) but is not a route any more",
				key, why)
		}
	}
}

// A route parameter carrying a character a client must percent-encode: an
// email address, which every one of these routes compares against stored
// data. chi matches on the raw path when it differs from the decoded one, so
// an undecoded parameter matches no row — the call answers as though the
// account did not exist, and the screen shows nothing happening.
func TestARouteParameterArrivesDecoded(t *testing.T) {
	s, srv := testServer(t)
	org := orgID(t, s, testSlug)
	hash, err := HashPassword("motdepasse-de-test-1234")
	if err != nil {
		t.Fatal(err)
	}
	const coord = "coord@exemple.fr"
	const target = "benevole@exemple.fr"
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
			"VALUES($1,$2,'Coordination',$3,'coordination',true)", org, coord, hash)
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
			"VALUES($1,$2,'Bénévole',$3,'volunteer',true)", org, target, hash)

	c := clientOn(t, srv, testSlug+".paraphe.test")
	if code := c.signIn(coord, "motdepasse-de-test-1234"); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	// Percent-encoded as a BROWSER sends it. Not url.PathEscape: `@` is a
	// legal path character, so that leaves the address untouched and this
	// test would exercise nothing — encodeURIComponent does escape it.
	encoded := strings.ReplaceAll(target, "@", "%40")
	code, rep := c.call(http.MethodPost,
		"/api/team/account/"+encoded+"/active", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("deactivating an account by its encoded address: %d %v", code, rep)
	}
	var active bool
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT active FROM accounts WHERE org_id=$1 AND email=$2",
			org, target).Scan(&active); err != nil {
			t.Fatal(err)
		}
	})
	if active {
		t.Error("the account is still active: the address arrived encoded and " +
			"matched no row, and the answer said nothing about it")
	}
}
