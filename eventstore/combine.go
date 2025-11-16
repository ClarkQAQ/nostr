package eventstore

import (
	"iter"

	"fiatjaf.com/nostr"
)

func SortedMerge(it1, it2 iter.Seq[nostr.Event]) iter.Seq[nostr.Event] {
	next1, done1 := iter.Pull(it1)
	next2, done2 := iter.Pull(it2)

	return func(yield func(nostr.Event) bool) {
		defer done1()
		defer done2()

		evt1, ok1 := next1()
		evt2, ok2 := next2()

	both:
		if ok1 && ok2 {
			if evt2.CreatedAt > evt1.CreatedAt {
				if !yield(evt2) {
					return
				}
				evt2, ok2 = next2()
				goto both
			} else {
				if !yield(evt1) {
					return
				}
				evt1, ok1 = next1()
				goto both
			}
		}

		if !ok2 {
		only1:
			if ok1 {
				if !yield(evt1) {
					return
				}
				evt1, ok1 = next1()
				goto only1
			}
		}

		if !ok1 {
		only2:
			if ok2 {
				if !yield(evt2) {
					return
				}
				evt2, ok2 = next2()
				goto only2
			}
		}
	}
}
