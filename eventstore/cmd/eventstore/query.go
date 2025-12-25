package main

import (
	"context"
	"fmt"
	"os"

	"fiatjaf.com/nostr"
	"github.com/mailru/easyjson"
	"github.com/urfave/cli/v3"
)

var query = &cli.Command{
	Name:        "query",
	ArgsUsage:   "[<filter-json>]",
	Usage:       "queries an eventstore for events, takes a filter as argument",
	Description: "applies the filter to the currently open eventstore, returning up to 10 million events.\n takes either a filter as an argument or reads a stream of filters from stdin.",
	Action: func(ctx context.Context, c *cli.Command) error {
		hasError := false
		for line := range getStdinLinesOrFirstArgument(c) {
			filter := nostr.Filter{}
			if err := easyjson.Unmarshal([]byte(line), &filter); err != nil {
				fmt.Fprintf(os.Stderr, "invalid filter '%s': %s\n", line, err)
				hasError = true
				continue
			}

			for evt := range db.QueryEvents(ctx, filter, 10_000_000) {
				fmt.Println(evt)
			}
		}

		if hasError {
			os.Exit(123)
		}
		return nil
	},
}
