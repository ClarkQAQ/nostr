package main

import (
	"context"
	"fmt"
	"os"

	"fiatjaf.com/nostr"
	"github.com/mailru/easyjson"
	"github.com/urfave/cli/v3"
)

var count = &cli.Command{
	Name:        "count",
	ArgsUsage:   "[<filter-json>]",
	Usage:       "counts all events that match a given filter",
	Description: "applies the filter to the currently open eventstore, counting the results",
	Action: func(ctx context.Context, c *cli.Command) error {
		hasError := false
		for line := range getStdinLinesOrFirstArgument(c) {
			filter := nostr.Filter{}
			if err := easyjson.Unmarshal([]byte(line), &filter); err != nil {
				fmt.Fprintf(os.Stderr, "invalid filter '%s': %s\n", line, err)
				hasError = true
				continue
			}

			res, err := db.CountEvents(ctx, filter)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to count '%s': %s\n", filter, err)
				hasError = true
				continue
			}
			fmt.Println(res)
		}

		if hasError {
			os.Exit(123)
		}
		return nil
	},
}
