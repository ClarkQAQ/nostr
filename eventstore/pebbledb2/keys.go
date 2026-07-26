package pebbledb

import (
	"encoding/binary"
	"io"
	"math"

	"fiatjaf.com/nostr"
	"github.com/cockroachdb/pebble/v2"
)

// Key layout (single byte prefix, big-endian fields so byte order == logical
// order). Every secondary index entry ends with ts(8) + id(32), which lets
// all scans share one decoding path and one k-way merge implementation.
//
//	'e' + id(32)                                  -> ts(8) locator (primary key / dedup / delete)
//	'v' + ts(8) + id(32)                          -> raw event JSON (body, time-clustered)
//	'p' + pubkey(32) + ts(8) + id(32)             -> author timeline        (pubkey, created_at)
//	'c' + pubkey(32) + kind(4) + ts(8) + id(32)   -> author+kind timeline   (pubkey, kind, created_at)
//	'k' + kind(4) + ts(8) + id(32)                -> kind timeline          (kind, created_at)
//	't' + nl(1) + name + vl(2) + value + ts(8) + id(32) -> tag index        (tag name+value, created_at)
//	'T' + ts(8) + id(32)                          -> global timeline        (created_at)
//
// Bodies are keyed by (ts, id) instead of id so that events created close
// in time share SST blocks. Relay traffic is heavily recency-biased: feed,
// thread and mention queries then hit the same small set of "recent" body
// blocks, which stay hot in the block cache. Index scans carry (ts, id)
// already, so they read bodies directly; only pure id lookups and deletes
// pay one extra locator hop.
//
// LSM prefix compression collapses the long shared prefixes (pubkey /
// tag value) inside SST blocks, so the on-disk overhead of the redundant
// 'p' and 'c' indexes is far smaller than the raw key sizes suggest.
const (
	pfxLocator   = 'e'
	pfxBody      = 'v'
	pfxPubkey    = 'p'
	pfxPubkeyKnd = 'c'
	pfxKind      = 'k'
	pfxTag       = 't'
	pfxTime      = 'T'
)

const (
	idLen   = 32
	tsLen   = 8
	tailLen = tsLen + idLen // every secondary index key ends with ts+id
)

// FormatVersion is written as a sentinel key at DB creation; changing
// it signals an incompatible on-disk format.
const FormatVersion int64 = 2

// versionKey is the magic key storing the format version.
var versionKey = []byte{0xFF, 0xFF, 'V', 'E', 'R', 'S', 'I', 'O', 'N'}

// encTS encodes a uint64 timestamp in 8 big-endian bytes for key encoding.
func encTS(ts uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], ts)
	return b[:]
}

// locatorKey is the primary-key entry: id -> created_at locator.
func locatorKey(id []byte) []byte {
	k := make([]byte, 1+idLen)
	k[0] = pfxLocator
	copy(k[1:], id)
	return k
}

// bodyKey stores the event JSON clustered by created_at.
func bodyKey(ts uint64, id []byte) []byte {
	k := make([]byte, 1+tsLen+idLen)
	k[0] = pfxBody
	binary.BigEndian.PutUint64(k[1:], ts)
	copy(k[1+tsLen:], id)
	return k
}

func pubkeyPrefix(pk []byte) []byte {
	k := make([]byte, 1+idLen)
	k[0] = pfxPubkey
	copy(k[1:], pk)
	return k
}

func pubkeyKindPrefix(pk []byte, kind uint32) []byte {
	k := make([]byte, 1+idLen+4)
	k[0] = pfxPubkeyKnd
	copy(k[1:], pk)
	binary.BigEndian.PutUint32(k[1+idLen:], kind)
	return k
}

func kindPrefix(kind uint32) []byte {
	k := make([]byte, 1+4)
	k[0] = pfxKind
	binary.BigEndian.PutUint32(k[1:], kind)
	return k
}

// tagPrefix encodes (name, value) with explicit lengths so arbitrary bytes
// (including 0x00) are safe inside values.
func tagPrefix(name, value string) []byte {
	k := make([]byte, 0, 1+1+len(name)+2+len(value))
	k = append(k, pfxTag, byte(len(name)))
	k = append(k, name...)
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(value)))
	k = append(k, l[:]...)
	k = append(k, value...)
	return k
}

func timePrefix() []byte { return []byte{pfxTime} }

// ---- rollup counters (NIP-45 fast COUNT) ----
//
// Counters live in the 'N' keyspace and are written in the SAME Pebble batch
// as the event + index keys, so they can never drift from the data even on
// crash. Both key forms end with a 4-byte day number; use timeBounds4() for
// counter prefix scans.
//
//	'N' + 0x01 + kind(4) + day(4)      -> uint64 count   (per kind per UTC day)
//	'N' + 0x02 + day(4)                -> uint64 count   (all kinds per UTC day)
//	'N' + 0x03 + kind(4) + hour(4)     -> uint64 count   (per kind per UTC hour)
//	'N' + 0x04 + hour(4)               -> uint64 count   (all kinds per UTC hour)
//	'N' + 0x05 + pubkey(32) + kind(4)  -> uint64 count   (per author+kind, all time)
//
// Two time tiers (hour + day) let any [since, until] range be answered from
// counters except for at most two partial edge hours, which are scanned.
const (
	pfxCounter      = 'N'
	counterKindDay  = 0x01
	counterDay      = 0x02
	counterKindHour = 0x03
	counterHour     = 0x04
	counterPkKind   = 0x05
)

const (
	secondsPerDay  = 86400
	secondsPerHour = 3600
)

func dayOf(ts int64) uint32  { return uint32(ts / secondsPerDay) }
func hourOf(ts int64) uint32 { return uint32(ts / secondsPerHour) }

func kindDayCounterPrefix(kind uint32) []byte {
	k := make([]byte, 1+1+4)
	k[0] = pfxCounter
	k[1] = counterKindDay
	binary.BigEndian.PutUint32(k[2:], kind)
	return k
}

func dayCounterPrefix() []byte { return []byte{pfxCounter, counterDay} }

func kindHourCounterPrefix(kind uint32) []byte {
	k := make([]byte, 1+1+4)
	k[0] = pfxCounter
	k[1] = counterKindHour
	binary.BigEndian.PutUint32(k[2:], kind)
	return k
}

func hourCounterPrefix() []byte { return []byte{pfxCounter, counterHour} }

func pkKindCounterKey(pk []byte, kind uint32) []byte {
	k := make([]byte, 1+1+idLen+4)
	k[0] = pfxCounter
	k[1] = counterPkKind
	copy(k[2:], pk)
	binary.BigEndian.PutUint32(k[2+idLen:], kind)
	return k
}

func counterKey(prefix []byte, day uint32) []byte {
	k := make([]byte, len(prefix)+4)
	copy(k, prefix)
	binary.BigEndian.PutUint32(k[len(prefix):], day)
	return k
}

func enc64(v int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return b[:]
}

func dec64(b []byte) int64 { return int64(binary.BigEndian.Uint64(b)) }

// counterMerger implements pebble.Merger for rollup counters: each operand
// is an 8-byte big-endian int64 delta; merging sums them. Using Merge
// instead of read-modify-write defers the read of the current value to
// compaction/iteration, removing the per-group s.db.Get from the hot write
// path.
type counterValueMerger struct {
	sum int64
}

func (m *counterValueMerger) MergeNewer(value []byte) error {
	if len(value) != 8 {
		return nil // tolerate malformed operand: skip
	}
	m.sum += dec64(value)
	return nil
}

func (m *counterValueMerger) MergeOlder(value []byte) error {
	if len(value) != 8 {
		return nil
	}
	m.sum += dec64(value)
	return nil
}

func (m *counterValueMerger) Finish(includesBase bool) ([]byte, io.Closer, error) {
	return enc64(m.sum), nil, nil
}

// CounterMerger is the pebble.Merger for all rollup counter keys (those
// under pfxCounter). Its name is persisted in the DB manifest; changing it
// later is a format-breaking change.
var CounterMerger = &pebble.Merger{
	Merge: func(key, value []byte) (pebble.ValueMerger, error) {
		m := &counterValueMerger{}
		if len(value) == 8 {
			m.sum = dec64(value)
		}
		return m, nil
	},
	Name: "nostr-counter-v1",
}

// appendTail appends ts(8)+id(32) to a prefix, producing a full index key.
func appendTail(prefix []byte, ts uint64, id []byte) []byte {
	k := make([]byte, len(prefix)+tailLen)
	copy(k, prefix)
	binary.BigEndian.PutUint64(k[len(prefix):], ts)
	copy(k[len(prefix)+tsLen:], id)
	return k
}

// timeBounds returns [lower, upper) bound keys restricting a prefix scan to
// the created_at range [since, until] (both inclusive). tsLen must match the
// timestamp width used in the target keys.
func timeBounds(prefix []byte, since, until int64) (lower, upper []byte) {
	lo := uint64(since)
	hi := uint64(until)
	lower = make([]byte, len(prefix)+tsLen)
	copy(lower, prefix)
	binary.BigEndian.PutUint64(lower[len(prefix):], lo)
	if hi == ^uint64(0) {
		upper = prefixSuccessor(prefix)
	} else {
		upper = make([]byte, len(prefix)+tsLen)
		copy(upper, prefix)
		binary.BigEndian.PutUint64(upper[len(prefix):], hi+1)
	}
	return lower, upper
}

// timeBounds4 returns [lower, upper) bound keys for 4-byte bucket fields
// (day/hour counters). Same semantics as timeBounds but uses 4-byte encoding.
func timeBounds4(prefix []byte, d0, d1 uint32) (lower, upper []byte) {
	lower = make([]byte, len(prefix)+4)
	copy(lower, prefix)
	binary.BigEndian.PutUint32(lower[len(prefix):], d0)
	if d1 == math.MaxUint32 {
		upper = prefixSuccessor(prefix)
	} else {
		upper = make([]byte, len(prefix)+4)
		copy(upper, prefix)
		binary.BigEndian.PutUint32(upper[len(prefix):], d1+1)
	}
	return lower, upper
}

// prefixSuccessor returns the smallest key strictly greater than every key
// starting with prefix, or nil if the prefix is all 0xFF.
func prefixSuccessor(prefix []byte) []byte {
	s := make([]byte, len(prefix))
	copy(s, prefix)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != 0xFF {
			s[i]++
			return s[:i+1]
		}
	}
	return nil
}

// indexKeysFor builds every secondary index key for an event. Both save and
// delete derive keys through this single function so they can never drift.
func indexKeysFor(ev *nostr.Event, indexedTag func(name string) bool) [][]byte {
	ts := uint64(ev.CreatedAt)
	idBin := ev.ID[:]
	pkBin := ev.PubKey[:]
	kind := uint32(ev.Kind)
	// base: p + c + k + T
	keys := make([][]byte, 0, 4+len(ev.Tags))
	keys = append(keys, appendTail(pubkeyPrefix(pkBin), ts, idBin))
	keys = append(keys, appendTail(pubkeyKindPrefix(pkBin, kind), ts, idBin))
	keys = append(keys, appendTail(kindPrefix(kind), ts, idBin))
	keys = append(keys, appendTail(timePrefix(), ts, idBin))
	seen := make(map[string]struct{}, len(ev.Tags))
	for _, t := range ev.Tags {
		if len(t) < 2 || !indexedTag(t[0]) {
			continue
		}
		if len(t[0]) > 255 || len(t[1]) > 65535 {
			continue
		}
		prefix := tagPrefix(t[0], t[1])
		sp := string(prefix)
		if _, dup := seen[sp]; dup {
			continue
		}
		seen[sp] = struct{}{}
		keys = append(keys, appendTail(prefix, ts, idBin))
	}
	return keys
}
