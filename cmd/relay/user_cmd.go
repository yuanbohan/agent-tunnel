package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"yuanbohan/tunnel/internal/relay/auth"
)

func runUserDelete(ctx context.Context, client operatorClient, cfg userDeleteConfig, stdout io.Writer) error {
	usernameNorm, err := auth.NormalizeUsername(cfg.Username)
	if err != nil {
		return err
	}
	if err := client.DeleteUser(ctx, usernameNorm); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "deleted %s\n", usernameNorm)
	return err
}

func runUserTier(ctx context.Context, client operatorClient, cfg userTierConfig, stdout io.Writer) error {
	usernameNorm, err := auth.NormalizeUsername(cfg.Username)
	if err != nil {
		return err
	}
	tier, err := auth.NormalizeSubscriptionTier(cfg.Tier)
	if err != nil {
		return err
	}
	updated, err := client.SetUserTier(ctx, usernameNorm, tier)
	if err != nil {
		return err
	}
	if cfg.JSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(updated)
	}
	_, err = fmt.Fprintf(stdout, "set %s tier %s -> %s\n", updated.Username, updated.PreviousTier, updated.Tier)
	return err
}
