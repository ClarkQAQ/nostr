package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

const (
	LINE_PROCESSING_ERROR = iota
)

func detect(dir string) (string, error) {
	mayBeMMM := false
	if n := strings.Index(dir, "/"); n > 0 {
		mayBeMMM = true
		dir = filepath.Dir(dir)
	}

	f, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !f.IsDir() {
		// only boltdb is a file for now
		return "boltdb", nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	if mayBeMMM {
		for _, entry := range entries {
			if entry.Name() == "mmmm" {
				if entries, err := os.ReadDir(filepath.Join(dir, "mmmm")); err == nil {
					for _, e := range entries {
						if strings.HasSuffix(e.Name(), ".mdb") {
							return "mmm", nil
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("undetected")
}

func getStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		read := bytes.NewBuffer(make([]byte, 0, 1000))
		_, err := io.Copy(read, os.Stdin)
		if err == nil {
			return read.String()
		}
	}
	return ""
}

func isPiped() bool {
	stat, _ := os.Stdin.Stat()
	return stat.Mode()&os.ModeCharDevice == 0
}

func getStdinLinesOrFirstArgument(c *cli.Command) chan string {
	// try the first argument
	target := c.Args().First()
	if target != "" {
		single := make(chan string, 1)
		single <- target
		close(single)
		return single
	}

	// try the stdin
	multi := make(chan string)
	writeStdinLinesOrNothing(multi)
	return multi
}

func writeStdinLinesOrNothing(ch chan string) (hasStdinLines bool) {
	if isPiped() {
		// piped
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 16*1024*1024), 256*1024*1024)
			for scanner.Scan() {
				ch <- strings.TrimSpace(scanner.Text())
			}
			close(ch)
		}()
		return true
	} else {
		// not piped
		return false
	}
}

func getFileLines(filename string) chan string {
	ch := make(chan string)
	go func() {
		f, err := os.Open(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open file '%s': %s\n", filename, err)
			close(ch)
			return
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 16*1024*1024), 256*1024*1024)
		for scanner.Scan() {
			ch <- strings.TrimSpace(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "error reading file '%s': %s\n", filename, err)
		}
		close(ch)
	}()
	return ch
}
