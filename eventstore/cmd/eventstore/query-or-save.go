package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"
)

// this is the default command when no subcommands are given, we will just try everything
var queryOrSave = &cli.Command{
	Hidden: true,
	Name:   "query-or-save",
	Action: func(ctx context.Context, c *cli.Command) error {
		line := getStdin()

		ee := &nostr.EventEnvelope{}
		re := &nostr.ReqEnvelope{}
		e := &nostr.Event{}
		f := &nostr.Filter{}
		if json.Unmarshal([]byte(line), ee) == nil && ee.Event.ID != nostr.ZeroID {
			return doSave(ctx, line, ee.Event)
		}
		if json.Unmarshal([]byte(line), e) == nil && e.ID != nostr.ZeroID {
			return doSave(ctx, line, *e)
		}
		if json.Unmarshal([]byte(line), re) == nil {
			return doQuery(ctx, &re.Filters[0])
		}
		if json.Unmarshal([]byte(line), f) == nil && len(f.String()) > 2 {
			return doQuery(ctx, f)
		}

		return fmt.Errorf("couldn't parse input '%s'", line)
	},
}

func doSave(ctx context.Context, line string, evt nostr.Event) error {
	if err := db.SaveEvent(ctx, evt); err != nil {
		return fmt.Errorf("failed to save event '%s': %s", line, err)
	}
	fmt.Fprintf(os.Stderr, "saved %s", evt.ID)
	return nil
}

func doQuery(ctx context.Context, f *nostr.Filter) error {
	for evt := range db.QueryEvents(ctx, *f, 1_000_000) {
		fmt.Println(evt)
	}
	return nil
}
