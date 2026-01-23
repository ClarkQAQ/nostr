package clickhousedb

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/ClickHouse/clickhouse-go/v2"
)

var _ eventstore.Store = (*ClickHouseBackend)(nil)

type ClickHouseBackend struct {
	DSN       string
	TableName string

	conn clickhouse.Conn
}

func NewStore(dsn, tableName string) *ClickHouseBackend {
	return &ClickHouseBackend{
		DSN:       dsn,
		TableName: tableName,
	}
}

func (b *ClickHouseBackend) Init(ctx context.Context) error {
	opts, e := clickhouse.ParseDSN(b.DSN)
	if e != nil {
		return fmt.Errorf("invalid dsn: %w", e)
	}

	conn, e := clickhouse.Open(opts)
	if e != nil {
		return fmt.Errorf("failed to connect to clickhouse: %w", e)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping clickhouse: %w", err)
	}
	b.conn = conn

	b.TableName = strings.TrimSpace(b.TableName)
	if len(b.TableName) < 1 {
		b.TableName = "events"
	}

	ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if e := b.conn.Exec(ctx, buildCreateTableSQL(b.TableName)); e != nil {
		return fmt.Errorf("failed to create table: %w", e)
	}

	return nil
}

func (b *ClickHouseBackend) Close() {
	if b.conn != nil {
		if e := b.conn.Close(); e != nil {
			slog.Warn("failed to close clickhouse connection", slog.String("error", e.Error()))
		}
	}
}

func (b *ClickHouseBackend) SaveEvent(ctx context.Context, evt nostr.Event) error {
	ctx, cancel := context.WithTimeoutCause(ctx,
		time.Second*30, errors.New("save_event took too long"))
	defer cancel()

	eventId := ""
	if e := b.conn.QueryRow(ctx,
		buildSelectSQL(b.TableName, []string{"id"}, "id = ?", "", 1),
		evt.ID.Hex()).Scan(&eventId); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return fmt.Errorf("failed to check duplicate: %w", e)
	} else if eventId == evt.ID.Hex() {
		return eventstore.ErrDupEvent
	}

	tagsJSON, e := json.Marshal(evt.Tags)
	if e != nil {
		return fmt.Errorf("failed to marshal tags: %w", e)
	}

	// flatten tags for index
	tagsIndex := flattenTags(evt.Tags)

	ctx, cancel = context.WithTimeoutCause(ctx,
		time.Second*30, errors.New("save_event took too long"))
	defer cancel()

	ctx = clickhouse.Context(ctx,
		clickhouse.WithAsync(false),
	)

	if e := b.conn.Exec(ctx, buildInsertSQL(b.TableName, []string{
		"id", "pubkey", "created_at", "kind",
		"content", "sig", "tags", "tags_index",
	}), evt.ID.Hex(),
		evt.PubKey.Hex(),
		int64(evt.CreatedAt),
		uint64(evt.Kind),
		evt.Content,
		hex.EncodeToString(evt.Sig[:]),
		string(tagsJSON),
		tagsIndex,
	); e != nil {
		return fmt.Errorf("failed to insert event: %w", e)
	}

	return nil
}

func flattenTags(tags nostr.Tags) []string {
	flat := make([]string, 0, len(tags))
	for _, tag := range tags {
		if len(tag) >= 2 {
			flat = append(flat, tag[0]+":"+tag[1])
		}
	}
	return flat
}

func (b *ClickHouseBackend) QueryEvents(ctx context.Context, filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if filter.Search != "" {
			return
		}

		limit := maxLimit
		if filter.Limit > 0 && filter.Limit < limit {
			limit = filter.Limit
		}
		if filter.LimitZero {
			limit = 0
		}
		if limit <= 0 {
			return
		}

		// build WHERE clause
		where, args := buildWhereClause(filter)

		ctx, cancel := context.WithTimeoutCause(ctx,
			time.Second*30, errors.New("query_events took too long"))
		defer cancel()

		rows, e := b.conn.Query(ctx,
			buildSelectSQL(b.TableName, []string{
				"id", "pubkey", "created_at", "kind", "content", "sig", "tags",
			}, where, "DESC", limit), args...)
		if e != nil {
			slog.Error("failed to query events", slog.String("error", e.Error()))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var idHex, pubkeyHex, content, sigHex, tagsJSON string
			var createdAt int64
			var kind uint64
			if e := rows.Scan(&idHex, &pubkeyHex, &createdAt, &kind, &content, &sigHex, &tagsJSON); e != nil {
				slog.Error("failed to scan event", slog.String("error", e.Error()))
				continue
			}

			// convert hex to bytes
			id, e := nostr.IDFromHex(idHex)
			if e != nil {
				slog.Error("failed to parse event id", slog.String("error", e.Error()))
				continue
			}
			pubkey, e := nostr.PubKeyFromHex(pubkeyHex)
			if e != nil {
				slog.Error("failed to parse pubkey", slog.String("error", e.Error()))
				continue
			}
			sig, e := hex.DecodeString(sigHex)
			if e != nil {
				slog.Error("failed to parse sig", slog.String("error", e.Error()))
				continue
			}

			evt := nostr.Event{
				ID:        id,
				PubKey:    pubkey,
				CreatedAt: nostr.Timestamp(createdAt),
				Kind:      nostr.Kind(kind),
				Content:   content,
			}

			if e := json.Unmarshal([]byte(tagsJSON), &evt.Tags); e != nil {
				slog.Error("failed to unmarshal tags", slog.String("error", e.Error()))
				continue
			}

			copy(evt.Sig[:], sig)

			if !yield(evt) {
				return
			}
		}
	}
}

func (b *ClickHouseBackend) DeleteEvent(ctx context.Context, id nostr.ID) error {
	ctx, cancel := context.WithTimeoutCause(ctx,
		time.Second*30, errors.New("delete_event took too long"))
	defer cancel()

	if e := b.conn.Exec(ctx, buildDeleteSQL(b.TableName, "id = ?"), id.Hex()); e != nil {
		return fmt.Errorf("failed to delete event: %w", e)
	}

	return nil
}

// ReplaceEvent implements eventstore.Store.
func (b *ClickHouseBackend) ReplaceEvent(ctx context.Context, evt nostr.Event) error {
	// build filter for replaceable events
	filter := nostr.Filter{Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
	if evt.Kind.IsAddressable() {
		d := evt.Tags.GetD()
		if d == "" {
			// not addressable? treat as normal replaceable
		} else {
			filter.Tags = nostr.TagMap{"d": []string{d}}
		}
	}

	// query existing events
	existing := b.QueryEvents(ctx, filter, 10)
	var toDelete []nostr.Event
	shouldStore := true
	for prev := range existing {
		if nostr.IsOlder(prev, evt) {
			toDelete = append(toDelete, prev)
		} else {
			// there is a newer event already stored
			shouldStore = false
		}
	}

	// delete older events
	for _, prev := range toDelete {
		if err := b.DeleteEvent(ctx, prev.ID); err != nil {
			return fmt.Errorf("failed to delete older event: %w", err)
		}
	}

	if shouldStore {
		return b.SaveEvent(ctx, evt)
	}
	return nil
}

func (b *ClickHouseBackend) CountEvents(ctx context.Context, filter nostr.Filter) (uint32, error) {
	where, args := buildWhereClause(filter)

	ctx, cancel := context.WithTimeoutCause(ctx,
		time.Second*60, errors.New("count_events took too long"))
	defer cancel()

	var count uint64
	if e := b.conn.QueryRow(ctx,
		buildSelectSQL(b.TableName, []string{"count(*)"}, where, "", 0),
		args...).Scan(&count); e != nil {
		return 0, fmt.Errorf("failed to count events: %w", e)
	}

	return uint32(count), nil
}
