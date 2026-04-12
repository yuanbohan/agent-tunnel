package main

import (
	"fmt"
	"io"

	"yuanbohan/tunnel/internal/buildinfo"
)

func writeVersion(w io.Writer) error {
	_, err := fmt.Fprintf(w, "relay %s\n", buildinfo.String())
	return err
}
