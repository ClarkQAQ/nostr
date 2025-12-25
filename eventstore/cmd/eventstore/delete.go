package main

import (
	"context"
	"fmt"
	"os"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"
)

var delete_ = &cli.Command{
	Name:        "delete",
	ArgsUsage:   "[<id>]",
	Usage:       "deletes an event by id and all its associated index entries",
	Description: "takes an id either as an argument or reads a stream of ids from stdin and deletes them from the currently open eventstore.",
	Action: func(ctx context.Context, c *cli.Command) error {
		hasError := false
		for line := range getStdinLinesOrFirstArgument(c) {
			id, err := nostr.IDFromHex(line)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid id '%s': %s\n", line, err)
				hasError = true
			}

			if err := db.DeleteEvent(ctx, id); err != nil {
				fmt.Fprintf(os.Stderr, "error deleting '%s': %s\n", id.Hex(), err)
				hasError = true
			}
		}

		if hasError {
			os.Exit(123)
		}
		return nil
	},
}
