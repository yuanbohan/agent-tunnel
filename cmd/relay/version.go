package main

import (
	"fmt"
	"io"

	"yuanbohan/tunnel/internal/buildinfo"
)

func writeVersion(w io.Writer) error {
	_, err := fmt.Fprintf(w, "relay %s\n", buildinfo.String())
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "branch: %s\n", buildinfo.GitBranch)
	fmt.Fprintf(w, "commit: %s\n", buildinfo.GitCommit)
	fmt.Fprintf(w, "build:  %s\n", buildinfo.BuildTime)
	return nil
}
