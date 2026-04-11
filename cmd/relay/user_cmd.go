package main

import (
	"context"
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
