package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"
	"text/tabwriter"

	"yuanbohan/tunnel/internal/relay/auth"
)

func runInviteCreate(ctx context.Context, client operatorClient, cfg inviteCreateConfig, stdout io.Writer) error {
	codes, err := client.CreateInvites(ctx, cfg.Count, cfg.ExpiresInDays)
	if err != nil {
		return err
	}
	for _, code := range codes {
		if _, err := fmt.Fprintln(stdout, code); err != nil {
			return err
		}
	}
	return nil
}

func runInviteDisable(ctx context.Context, client operatorClient, cfg inviteDisableConfig, stdout io.Writer) error {
	code, err := auth.NormalizeInviteCode(cfg.Code)
	if err != nil {
		return err
	}
	if err := client.DisableInvite(ctx, code); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "disabled %s\n", code)
	return err
}

func runInviteList(ctx context.Context, client operatorClient, _ inviteListConfig, stdout io.Writer) error {
	invites, err := client.ListInvites(ctx)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CODE\tSTATUS\tCONSUMED_BY\tDISABLED_BY\tEXPIRES_AT")

	for _, invite := range invites {
		status := "available"
		switch {
		case invite.Disabled:
			status = "disabled"
		case invite.Expired:
			status = "expired"
		case invite.Consumed:
			status = "consumed"
		}
		owner := "-"
		if invite.ConsumedByUsername != "" {
			owner = invite.ConsumedByUsername
		} else if invite.ConsumedByUserID != nil {
			owner = strconv.FormatInt(*invite.ConsumedByUserID, 10)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			invite.Code,
			status,
			owner,
			nullableString(invite.DisabledBy),
			time.Unix(invite.ExpiresAt, 0).UTC().Format(time.RFC3339),
		)
	}
	return tw.Flush()
}

func nullableString(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
