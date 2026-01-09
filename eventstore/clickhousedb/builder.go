package clickhousedb

import (
	"fmt"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS %s (
	id String,
	pubkey String,
	created_at DateTime64(0),
	kind UInt64,
	content String,
	sig String,
	tags String,
	tags_index Array(String),
	INDEX idx_id id TYPE bloom_filter GRANULARITY 1,
	INDEX idx_pubkey pubkey TYPE bloom_filter GRANULARITY 1,
	INDEX idx_kind kind TYPE minmax GRANULARITY 1,
	INDEX idx_tags tags_index TYPE bloom_filter GRANULARITY 1
) ENGINE = MergeTree()
ORDER BY (created_at, kind, pubkey)
SETTINGS index_granularity = 8192, wait_for_async_insert = 1;`

func buildCreateTableSQL(tableName string) string {
	return fmt.Sprintf(strings.Join(strings.Fields(createTableSQL), " "), tableName)
}

func buildInsertSQL(tableName string, fields []string) string {
	builder := strings.Builder{}
	builder.WriteString("INSERT INTO ")
	builder.WriteString(tableName)
	builder.WriteString(" (")
	builder.WriteString(strings.Join(fields, ","))
	builder.WriteString(") VALUES (")
	for i := range fields {
		builder.WriteString("?")
		if i < len(fields)-1 {
			builder.WriteString(",")
		}
	}
	builder.WriteString(")")
	return builder.String()
}

func buildSelectSQL(tableName string, fields []string, where string, sort string, limit int) string {
	builder := strings.Builder{}
	builder.WriteString("SELECT ")
	builder.WriteString(strings.Join(fields, ","))
	builder.WriteString(" FROM ")
	builder.WriteString(tableName)
	if where != "" {
		builder.WriteString(" WHERE ")
		builder.WriteString(where)
	}
	if sort != "" {
		builder.WriteString(" ORDER BY created_at ")
		builder.WriteString(sort)
	}
	if limit > 0 {
		builder.WriteString(" LIMIT ")
		builder.WriteString(strconv.FormatInt(int64(limit), 10))
	}
	return builder.String()
}

func buildWhereClause(filter nostr.Filter) (string, []interface{}) {
	var conditions []string
	var args []interface{}

	if len(filter.IDs) > 0 {
		placeholders := make([]string, len(filter.IDs))
		for i, id := range filter.IDs {
			placeholders[i] = "?"
			args = append(args, id.Hex())
		}
		conditions = append(conditions, "id IN ("+strings.Join(placeholders, ",")+")")
	}

	if len(filter.Authors) > 0 {
		placeholders := make([]string, len(filter.Authors))
		for i, pk := range filter.Authors {
			placeholders[i] = "?"
			args = append(args, pk.Hex())
		}
		conditions = append(conditions, "pubkey IN ("+strings.Join(placeholders, ",")+")")
	}

	if len(filter.Kinds) > 0 {
		placeholders := make([]string, len(filter.Kinds))
		for i, k := range filter.Kinds {
			placeholders[i] = "?"
			args = append(args, k.Num())
		}
		conditions = append(conditions, "kind IN ("+strings.Join(placeholders, ",")+")")
	}

	for key, values := range filter.Tags {
		if len(values) == 0 {
			continue
		}
		var tagConditions []string
		for _, v := range values {
			tagConditions = append(tagConditions, "has(tags_index, ?)")
			args = append(args, key+":"+v)
		}
		conditions = append(conditions, "("+strings.Join(tagConditions, " AND ")+")")
	}

	if filter.Since > 0 {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, filter.Since)
	}

	if filter.Until > 0 {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, filter.Until)
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return strings.Join(conditions, " AND "), args
}

func buildDeleteSQL(tableName string, where string) string {
	return fmt.Sprintf("ALTER TABLE %s DELETE WHERE %s", tableName, where)
}
