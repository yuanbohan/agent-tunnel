package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

const prompt = "PROMPT> "

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "e2e-launcher: %v\n", err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer) error {
	if _, err := io.WriteString(output, "READY e2e-launcher\r\n"+prompt); err != nil {
		return err
	}

	reader := bufio.NewReader(input)
	line := make([]byte, 0, 64)
	lastWasCR := false

	for {
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch b {
		case '\r':
			if err := flushCommand(output, line); err != nil {
				return err
			}
			line = line[:0]
			lastWasCR = true
		case '\n':
			if lastWasCR {
				lastWasCR = false
				continue
			}
			if err := flushCommand(output, line); err != nil {
				return err
			}
			line = line[:0]
		default:
			lastWasCR = false
			line = append(line, b)
		}
	}
}

func flushCommand(output io.Writer, line []byte) error {
	command := string(line)
	if command == "" {
		_, err := io.WriteString(output, "\r\nEMPTY\r\n"+prompt)
		return err
	}
	if command == "exit" {
		_, err := io.WriteString(output, "\r\nBYE\r\n")
		return err
	}

	_, err := fmt.Fprintf(output, "\r\nREPLY %s\r\n%s", command, prompt)
	return err
}
