package mmm

import (
	"bytes"
	"encoding/binary"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

type EventStats struct {
	Total     uint
	PerWeek   []uint
	PerPubKey map[nostr.PubKey]PubKeyStats
	PerKind   map[nostr.Kind]KindStats
}

type KindStats struct {
	Total   uint
	PerWeek []uint
}

type PubKeyStats struct {
	Total          uint
	PerWeek        []uint
	PerKind        map[nostr.Kind]uint
	PerKindPerWeek map[nostr.Kind][]uint
}

type StatsOptions struct {
	OnlyPubKey nostr.PubKey
}

func (il *IndexingLayer) ComputeStats(opts StatsOptions) (EventStats, error) {
	stats := EventStats{
		Total:     0,
		PerWeek:   make([]uint, 0, 24),
		PerPubKey: make(map[nostr.PubKey]PubKeyStats, 30),
		PerKind:   make(map[nostr.Kind]KindStats, 20),
	}

	err := il.lmdbEnv.View(func(txn *lmdb.Txn) error {
		txn.RawRead = true

		cursor, err := txn.OpenCursor(il.indexPubkeyKind)
		if err != nil {
			return err
		}
		defer cursor.Close()

		var currentPubKeyPrefix []byte
		var currentPubKey nostr.PubKey

		// position cursor based on options
		var initialKey []byte
		if opts.OnlyPubKey != nostr.ZeroPK {
			// position cursor at the start of this author's data
			initialKey = make([]byte, 8+4+4)
			copy(initialKey[0:8], opts.OnlyPubKey[0:8])
		}

		var key []byte
		var val []byte
		if initialKey == nil {
			key, val, err = cursor.Get(nil, nil, lmdb.Next)
			if lmdb.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
		} else {
			key, val, err = cursor.Get(initialKey, nil, lmdb.SetRange)
			if err != nil {
				return err
			}
		}

		for {
			// parse key: [8 bytes pubkey][2 bytes kind][4 bytes timestamp]
			pubkeyPrefix := key[0:8]
			if !bytes.Equal(pubkeyPrefix, currentPubKeyPrefix) {
				if opts.OnlyPubKey != nostr.ZeroPK && len(currentPubKeyPrefix) > 0 {
					// stop scanning now as we're filtering for a specific pubkey
					break
				}

				// load pubkey from event (otherwise will use the same from before)
				pos := positionFromBytes(val)
				currentPubKey = betterbinary.GetPubKey(il.mmmm.mmapf[pos.start : pos.start+uint64(pos.size)])
				currentPubKeyPrefix = pubkeyPrefix
			}

			kind := nostr.Kind(binary.BigEndian.Uint16(key[8:10]))
			createdTime := time.Unix(int64(binary.BigEndian.Uint32(key[10:14])), 0)

			// figure out how many weeks in the past this is
			weekIndex := weeksInPast(createdTime)

			// update totals
			stats.Total++
			if weekIndex >= 0 {
				for len(stats.PerWeek) <= weekIndex {
					stats.PerWeek = append(stats.PerWeek, 0)
				}
				stats.PerWeek[weekIndex]++
			}
			if this, exists := stats.PerPubKey[currentPubKey]; exists {
				this.Total++
				this.PerKind[kind]++
				if weekIndex >= 0 {
					for len(this.PerWeek) <= weekIndex {
						this.PerWeek = append(this.PerWeek, 0)
					}
					this.PerWeek[weekIndex]++
				}
				stats.PerPubKey[currentPubKey] = this
			} else {
				stats.PerPubKey[currentPubKey] = PubKeyStats{
					Total: 1,
					PerKind: map[nostr.Kind]uint{
						kind: 1,
					},
				}
			}
			if this, exists := stats.PerKind[kind]; exists {
				this.Total++
				if weekIndex >= 0 {
					for len(this.PerWeek) <= weekIndex {
						this.PerWeek = append(this.PerWeek, 0)
					}
					this.PerWeek[weekIndex]++
				}
				stats.PerKind[kind] = this
			} else {
				stats.PerKind[kind] = KindStats{
					Total: 1,
				}
			}

			key, val, err = cursor.Get(nil, nil, lmdb.Next)
			if lmdb.IsNotFound(err) {
				break
			}
			if err != nil {
				return err
			}
		}

		return nil
	})

	return stats, err
}

func weeksInPast(date time.Time) int {
	now := time.Now()

	if date.After(now) {
		// when in the future always return -1
		return -1
	}

	lastSaturday := now.AddDate(0, 0, -int(now.Weekday()+1))
	lastSaturday = time.Date(lastSaturday.Year(), lastSaturday.Month(), lastSaturday.Day(), 23, 59, 59, 0, lastSaturday.Location())

	// if the date is after the last completed Saturday, it's in the current incomplete week
	if date.After(lastSaturday) {
		return 0
	}

	// calculate the number of complete weeks between the date and last saturday
	daysDiff := int(lastSaturday.Sub(date).Hours() / 24)
	completeWeeks := (daysDiff / 7) + 1 // +1 because we've already passed at least one Saturday

	return completeWeeks
}
