package main

import (
	"context"
	"fmt"
	"io"

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
