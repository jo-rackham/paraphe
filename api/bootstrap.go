package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
)

// bootstrap: without a coordination account, nobody can enter and nobody
// can create one — the app says so instead of opening up. Multi-campaign,
// an instance administrator is also required, the only role allowed to
// approve campaign requests: without one, the public form piles up
// requests nobody can process.
func bootstrap(ctx context.Context, tx pgx.Tx, bootstrapOrg int) error {
	if err := bootstrapAdministration(ctx, tx); err != nil {
		return err
	}
	if bootstrapOrg < 1 {
		// multi-campaign instance without a bootstrap campaign: normal,
		// campaigns are born from approved requests
		return nil
	}

	email := normalizeEmail(Get("admin_email"))
	password, err := UsableSecret(Get("admin_password"),
		"PARAPHE_ADMIN_PASSWORD")
	if err != nil {
		return err
	}
	if email != "" && password != "" {
		if err := seedAccount(ctx, tx, bootstrapOrg, email,
			Get("admin_name"), password, RoleCoordination); err != nil {
			return err
		}
		slog.Info("coordination account", "email", email)
		return nil
	}
	var one int
	err = tx.QueryRow(ctx,
		"SELECT 1 FROM accounts WHERE org_id=$1 AND role='coordination' AND active",
		bootstrapOrg).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		// Refuse to start rather than serve a campaign nobody can ever
		// enter. `cp .env.exemple .env` leaves PARAPHE_ADMIN_PASSWORD
		// empty, and the application then came up, answered every request,
		// and offered a sign-in form no password opens — with nothing in
		// the logs. DEPLOYMENT.md promises the opposite.
		return fmt.Errorf("no coordination account, and none can be created: "+
			"set PARAPHE_ADMIN_EMAIL and PARAPHE_ADMIN_PASSWORD (currently "+
			"%q and %s). Without them nobody could ever sign in",
			email, describeSecret(Get("admin_password")))
	}
	if err != nil {
		return fmt.Errorf("looking up a coordination account: %w", err)
	}
	return nil
}

// describeSecret says whether a secret is set WITHOUT printing it.
func describeSecret(v string) string {
	if strings.TrimSpace(v) == "" {
		return "an empty password"
	}
	return "a password that was refused"
}

// bootstrapAdministration creates the instance administrator. It lives in
// the instance scope (organisation 0), which sees NO work row: it moderates
// campaign requests, it does not read volunteers' notes.
func bootstrapAdministration(ctx context.Context, tx pgx.Tx) error {
	email := normalizeEmail(Get("instance_admin_email"))
	password, err := UsableSecret(Get("instance_admin_password"),
		"PARAPHE_INSTANCE_ADMIN_PASSWORD")
	if err != nil {
		return err
	}
	if email == "" || password == "" {
		if BaseDomain() != "" {
			var one int
			err := tx.QueryRow(ctx,
				"SELECT 1 FROM accounts WHERE org_id=$1 AND role=$2 AND active",
				OrgInstance, RoleAdministration).Scan(&one)
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Warn("NO INSTANCE ADMINISTRATOR: campaign requests will " +
					"pile up with nobody able to approve them. Set " +
					"PARAPHE_INSTANCE_ADMIN_EMAIL and " +
					"PARAPHE_INSTANCE_ADMIN_PASSWORD.")
			} else if err != nil {
				return fmt.Errorf("looking up an instance administrator: %w", err)
			}
		}
		return nil
	}
	if err := seedAccount(ctx, tx, OrgInstance, email,
		Get("instance_admin_name"), password,
		RoleAdministration); err != nil {
		return err
	}
	slog.Info("instance administrator", "email", email)
	return nil
}

func seedAccount(ctx context.Context, tx pgx.Tx, org int,
	email, name, password, role string) error {
	hashed, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, created_at, created_by) "+
			"VALUES($1,$2,$3,$4,$5,$6,'amorçage') "+
			"ON CONFLICT(org_id, email) DO UPDATE SET password_hash=excluded.password_hash, "+
			"role=excluded.role, active=TRUE",
		org, email, name, hashed, role, shortTimestamp()); err != nil {
		return fmt.Errorf("seeding account %s (%s): %w", email, role, err)
	}
	return nil
}
