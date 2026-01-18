package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"github.com/urfave/cli/v3"
)

var (
	db  eventstore.Store
	end func()
)

var app = &cli.Command{
	Name:      "eventstore",
	Usage:     "a CLI for all the eventstore backends",
	UsageText: "eventstore -d ./data <query|save|delete> ...",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "store",
			Aliases:  []string{"d"},
			Usage:    "path to the database file or directory or database connection uri",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "type",
			Aliases: []string{"t"},
			Usage:   "store type ('lmdb', 'boltdb', 'mmm')",
		},
	},
	Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
		path := strings.TrimSuffix(c.String("store"), "/")
		typ := c.String("type")
		if typ != "" {
			// bypass automatic detection
			// this also works for creating disk databases from scratch
		} else {
			// try to detect based on url scheme
			switch {
			case strings.HasPrefix(path, "postgres://"), strings.HasPrefix(path, "postgresql://"):
				typ = "postgres"
			case strings.HasPrefix(path, "mysql://"):
				typ = "mysql"
			case strings.HasPrefix(path, "https://"):
				// if we ever add something else that uses URLs we'll have to modify this
				typ = "elasticsearch"
			case strings.HasSuffix(path, ".jsonl"):
				typ = "file"
			default:
				// try to detect based on the form and names of disk files
				dbname, err := detect(path)
				if err != nil {
					if os.IsNotExist(err) {
						return ctx, fmt.Errorf(
							"'%s' does not exist, to create a store there specify the --type argument", path)
					}
					return ctx, fmt.Errorf("failed to detect store type: %w", err)
				}
				typ = dbname
			}
		}

		switch typ {
		case "lmdb":
			db = &lmdb.LMDBBackend{Path: path}
			if err := db.Init(); err != nil {
				return ctx, err
			}
			end = db.Close
		case "boltdb":
			db = &boltdb.BoltBackend{Path: path}
			if err := db.Init(); err != nil {
				return ctx, err
			}
			end = db.Close
		case "mmm":
			var err error
			readonly := c.Args().First() == "query" || c.Args().First() == "count"
			if db, err, end = doMmmInit(path, readonly); err != nil {
				if end != nil {
					end()
				}
				return ctx, err
			}
		case "file":
			db = &slicestore.SliceStore{}
			if err := db.Init(); err != nil {
				return ctx, err
			}

			// fill in the slicestore with the data from the file
			f, err := os.Open(path)
			if err != nil {
				log.Printf("failed to file at '%s': %s\n", path, err)
				os.Exit(3)
			}
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 16*1024*1024), 256*1024*1024)
			i := 0
			for scanner.Scan() {
				var evt nostr.Event
				if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
					log.Printf("invalid event read at line %d: %s (`%s`)\n", i, err, scanner.Text())
				}
				db.SaveEvent(evt)
				i++
			}

			end = func() {
				db.Close()
				f.Close()
			}
		case "":
			return ctx, fmt.Errorf("couldn't determine store type, you can use --type to specify it manually")
		default:
			return ctx, fmt.Errorf("'%s' store type is not supported by this CLI", typ)
		}

		return ctx, nil
	},
	Commands: []*cli.Command{
		queryOrSave,
		query,
		count,
		save,
		delete_,
		neg,
	},
	DefaultCommand: "query-or-save",
}

func main() {
	defer func() { end() }()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
