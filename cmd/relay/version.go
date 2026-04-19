package main

import (
	"fmt"
	"io"

	"yuanbohan/tunnel/internal/buildinfo"
)

// writeVersion writes the current application version and git metadata to the writer.
func writeVersion(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "relay %s\n", buildinfo.String()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "branch: %s\n", buildinfo.GitBranch); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "commit: %s\n", buildinfo.GitCommit); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "build:  %s\n", buildinfo.BuildTime); err != nil {
		return err
	}
	return nil
}
