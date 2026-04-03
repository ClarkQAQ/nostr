package mmm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"unsafe"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/rs/zerolog"
)

type mmap []byte

func (_ mmap) String() string { return "<memory-mapped file>" }

var ReadOnly = errors.New("mmm in read-only mode")

type MultiMmapManager struct {
	Dir      string
	Logger   *zerolog.Logger
	ReadOnly bool

	layers IndexingLayers

	mmapfPath string
	mmapf     mmap
	mmapfEnd  uint64

	writeMutex sync.Mutex
	lockfile   *os.File

	lmdbEnv     *lmdb.Env
	stuff       lmdb.DBI
	knownLayers lmdb.DBI
	indexId     lmdb.DBI

	freeRangesAll   positions  // sorted by position
	freeRangesLarge []position // unsorted
}

func (b *MultiMmapManager) String() string {
	return fmt.Sprintf("<MultiMmapManager on %s with %d layers @ %v>", b.Dir, len(b.layers), unsafe.Pointer(b))
}

const (
	MMAP_INFINITE_SIZE = 100_000_000_000
	maxuint16          = 65535
	maxuint32          = 4294967295
)

func (b *MultiMmapManager) Init() (err error) {
	if b.Logger == nil {
		nopLogger := zerolog.Nop()
		b.Logger = &nopLogger
	}

	defer func() {
		if err != nil {
			b.releaseLock()
		}
	}()

	// create directory if it doesn't exist
	dbpath := filepath.Join(b.Dir, "mmmm")
	if err := os.MkdirAll(dbpath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dbpath, err)
	}

	// lock database directory to prevent multiple instances
	lockfilePath := filepath.Join(b.Dir, "mmmm.lock")
	lockfile, err := os.OpenFile(lockfilePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open lockfile %s: %w", lockfilePath, err)
	}
	if err := syscall.Flock(int(lockfile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockfile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("database at %s is already in use by another instance", b.Dir)
		}
		return fmt.Errorf("failed to lock database at %s: %w", b.Dir, err)
	}
	b.lockfile = lockfile

	// open a huge mmapped file
	b.mmapfPath = filepath.Join(b.Dir, "events")
	file, err := os.OpenFile(b.mmapfPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open events file at %s: %w", b.mmapfPath, err)
	}
	mmapf, err := syscall.Mmap(int(file.Fd()), 0, int(MMAP_INFINITE_SIZE),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("failed to mmap events file at %s: %w", b.mmapfPath, err)
	}
	b.mmapf = mmapf

	if stat, err := os.Stat(b.mmapfPath); err != nil {
		return err
	} else {
		b.mmapfEnd = uint64(stat.Size())
	}

	// open lmdb
	env, err := lmdb.NewEnv()
	if err != nil {
		return err
	}

	env.SetMaxDBs(3)
	env.SetMaxReaders(1000)
	env.SetMapSize(MMAP_INFINITE_SIZE)

	err = env.Open(dbpath, lmdb.NoTLS, 0644)
	if err != nil {
		return fmt.Errorf("failed to open lmdb at %s: %w", dbpath, err)
	}
	b.lmdbEnv = env

	if err := b.lmdbEnv.Update(func(txn *lmdb.Txn) error {
		if dbi, err := txn.OpenDBI("stuff", lmdb.Create); err != nil {
			return err
		} else {
			b.stuff = dbi
		}

		// this just keeps track of all the layers we know (just their names)
		// they will be instantiated by the application after their name is read from the database.
		// new layers created at runtime will be saved here.
		if dbi, err := txn.OpenDBI("layers", lmdb.Create); err != nil {
			return err
		} else {
			b.knownLayers = dbi
		}

		// this is a global index of events by id that also keeps references
		// to all the layers that may be indexing them -- such that whenever
		// an event is deleted from all layers it can be deleted from global
		if dbi, err := txn.OpenDBI("id-references", lmdb.Create); err != nil {
			return err
		} else {
			b.indexId = dbi
		}

		if !b.ReadOnly {
			// scan index table to calculate free ranges from used positions
			if err := b.gatherFreeRanges(txn); err != nil {
				return err
			}

			logOp := b.Logger.Debug()
			count := 0
			for _, pos := range b.freeRangesLarge {
				logOp = logOp.Uint32(fmt.Sprintf("%d", pos.start), pos.size)
				count++
			}
			logOp.Int("count", count).Msg("calculated free ranges from index scan")
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to open and load db data: %w", err)
	}

	return nil
}

func (b *MultiMmapManager) EnsureLayer(name string) (*IndexingLayer, error) {
	b.writeMutex.Lock()
	defer b.writeMutex.Unlock()

	il := &IndexingLayer{
		mmmm: b,
		name: name,
	}

	var err error
	if b.ReadOnly {
		err = b.lmdbEnv.View(func(txn *lmdb.Txn) error {
			txn.RawRead = true

			nameb := []byte(name)
			if idv, err := txn.Get(b.knownLayers, nameb); err == nil {
				if err := il.Init(); err != nil {
					return fmt.Errorf("failed to init read-only layer %s: %w", name, err)
				}
				il.id = binary.BigEndian.Uint16(idv)
				return nil
			} else {
				return err
			}
		})
	} else {
		err = b.lmdbEnv.Update(func(txn *lmdb.Txn) error {
			txn.RawRead = true

			nameb := []byte(name)
			if idv, err := txn.Get(b.knownLayers, nameb); lmdb.IsNotFound(err) {
				if id, err := b.getNextAvailableLayerId(txn); err != nil {
					return fmt.Errorf("failed to reserve a layer id for %s: %w", name, err)
				} else {
					il.id = id
				}

				if err := il.Init(); err != nil {
					return fmt.Errorf("failed to init new layer %s: %w", name, err)
				}

				return txn.Put(b.knownLayers, []byte(name), binary.BigEndian.AppendUint16(nil, il.id), 0)
			} else if err == nil {
				il.id = binary.BigEndian.Uint16(idv)

				if err := il.Init(); err != nil {
					return fmt.Errorf("failed to init old layer %s: %w", name, err)
				}

				return nil
			} else {
				return err
			}
		})
	}
	if err != nil {
		return nil, err
	}

	b.layers = append(b.layers, il)
	return il, nil
}

func (b *MultiMmapManager) DropLayer(name string) error {
	if b.ReadOnly {
		return ReadOnly
	}

	b.writeMutex.Lock()
	defer b.writeMutex.Unlock()

	// get layer reference
	idx := slices.IndexFunc(b.layers, func(il *IndexingLayer) bool { return il.name == name })
	if idx == -1 {
		return fmt.Errorf("layer '%s' doesn't exist", name)
	}
	il := b.layers[idx]

	// remove layer references
	err := b.lmdbEnv.Update(func(txn *lmdb.Txn) error {
		if err := b.removeAllReferencesFromLayer(txn, il.id); err != nil {
			return err
		}

		return txn.Del(b.knownLayers, []byte(il.name), nil)
	})
	if err != nil {
		return err
	}

	// delete everything (the indexes) from this layer db actually
	err = il.lmdbEnv.Update(func(txn *lmdb.Txn) error {
		for _, dbi := range []lmdb.DBI{
			il.indexCreatedAt,
			il.indexKind,
			il.indexPubkey,
			il.indexPubkeyKind,
			il.indexTag,
			il.indexTag32,
			il.indexTagAddr,
			il.indexPTagKind,
		} {
			if err := txn.Drop(dbi, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	return il.lmdbEnv.Close()
}

func (b *MultiMmapManager) removeAllReferencesFromLayer(txn *lmdb.Txn, layerId uint16) error {
	cursor, err := txn.OpenCursor(b.indexId)
	if err != nil {
		return fmt.Errorf("when opening cursor on %v: %w", b.indexId, err)
	}
	defer cursor.Close()

	for {
		idPrefix8, val, err := cursor.Get(nil, nil, lmdb.Next)
		if lmdb.IsNotFound(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("when moving the cursor: %w", err)
		}

		var zeroRefs bool
		var update bool

		needle := binary.BigEndian.AppendUint16(nil, layerId)
		for s := 12; s < len(val); s += 2 {
			if slices.Equal(val[s:s+2], needle) {
				// swap delete
				copy(val[s:s+2], val[len(val)-2:])
				val = val[0 : len(val)-2]

				update = true

				// we must erase this event if its references reach zero
				zeroRefs = len(val) == 12

				break
			}
		}

		if zeroRefs {
			posb := val[0:12]
			pos := positionFromBytes(posb)

			if err := txn.Del(b.indexId, idPrefix8, nil); err != nil {
				return fmt.Errorf("failed to purge unreferenced event %x: %w", idPrefix8, err)
			}

			b.mergeNewFreeRange(pos)
		} else if update {
			if err := txn.Put(b.indexId, idPrefix8, val, 0); err != nil {
				return fmt.Errorf("failed to put updated index+refs: %w", err)
			}
		}
	}

	return nil
}

func (b *MultiMmapManager) loadEvent(pos position, eventReceiver *nostr.Event) error {
	return betterbinary.Unmarshal(b.mmapf[pos.start:pos.start+uint64(pos.size)], eventReceiver)
}

// getNextAvailableLayerId iterates through all existing layers to find a vacant id
func (b *MultiMmapManager) getNextAvailableLayerId(txn *lmdb.Txn) (uint16, error) {
	cursor, err := txn.OpenCursor(b.knownLayers)
	if err != nil {
		return 0, fmt.Errorf("failed to open cursor: %w", err)
	}

	used := [1 << 16]bool{}
	_, val, err := cursor.Get(nil, nil, lmdb.First)
	for err == nil {
		// something was found
		used[binary.BigEndian.Uint16(val)] = true
		// next
		_, val, err = cursor.Get(nil, nil, lmdb.Next)
	}
	if !lmdb.IsNotFound(err) {
		// a real error
		return 0, err
	}

	// loop exited, get the first available
	var id uint16
	for num, isUsed := range used {
		if !isUsed {
			id = uint16(num)
			break
		}
	}
	return id, nil
}

func (b *MultiMmapManager) releaseLock() {
	if b.lockfile == nil {
		return
	}

	_ = syscall.Flock(int(b.lockfile.Fd()), syscall.LOCK_UN)
	_ = b.lockfile.Close()
	b.lockfile = nil

	lockfilePath := filepath.Join(b.Dir, "mmmm.lock")
	_ = os.Remove(lockfilePath)
}

func (b *MultiMmapManager) Close() {
	b.lmdbEnv.Close()
	for _, il := range b.layers {
		il.Close()
	}

	syscall.Munmap(b.mmapf)

	b.releaseLock()
}
