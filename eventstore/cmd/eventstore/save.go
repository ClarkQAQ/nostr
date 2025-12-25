package main

import (
	"context"
	"fmt"
	"os"

	"fiatjaf.com/nostr"
	"github.com/mailru/easyjson"
	"github.com/urfave/cli/v3"
)

var save = &cli.Command{
	Name:        "save",
	ArgsUsage:   "[<event-json>]",
	Usage:       "stores an event",
	Description: "takes either an event as an argument, reads a stream of events from stdin, or from a file specified with --file, and inserts those in the currently opened eventstore.\ndoesn't perform any kind of signature checking or replacement.",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "file",
			Aliases: []string{"f"},
			Usage:   "file to read events from",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		hasError := false
		var lines chan string
		if file := c.String("file"); file != "" {
			lines = getFileLines(file)
		} else {
			lines = getStdinLinesOrFirstArgument(c)
		}
		for line := range lines {
			var event nostr.Event
			if err := easyjson.Unmarshal([]byte(line), &event); err != nil {
				fmt.Fprintf(os.Stderr, "invalid event '%s': %s\n", line, err)
				hasError = true
				continue
			}

			if err := db.SaveEvent(ctx, event); err != nil {
				fmt.Fprintf(os.Stderr, "failed to save event '%s': %s\n", line, err)
				hasError = true
				continue
			}

			fmt.Fprintf(os.Stderr, "saved %s\n", event.ID)
		}

		if hasError {
			os.Exit(123)
		}
		return nil
	},
}
