package nostr

import (
	stdlibjson "encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
	"unsafe"
)

func BenchmarkParseMessage(b *testing.B) {
	for _, name := range []string{"relay", "client"} {
		b.Run(name, func(b *testing.B) {
			messages := generateTestMessages(name)

			b.Run("jsonstdlib", func(b *testing.B) {
				for b.Loop() {
					for _, msg := range messages {
						var v any
						_ = stdlibjson.Unmarshal(unsafe.Slice(unsafe.StringData(msg), len(msg)), &v)
					}
				}
			})

			b.Run("easyjson", func(b *testing.B) {
				for b.Loop() {
					for _, msg := range messages {
						_, _ = ParseMessage(msg)
					}
				}
			})
		})
	}
}

func generateTestMessages(typ string) []string {
	messages := make([]string, 0, 600)

	setup := map[string]map[int]func() []byte{
		"client": {
			600: generateEventMessage,
			5:   generateEOSEMessage,
			9:   generateNoticeMessage,
			14:  generateCountMessage,
			20:  generateOKMessage,
		},
		"relay": {
			500: generateReqMessage,
			50:  generateEventMessage,
			10:  generateCountMessage,
		},
	}[typ]

	for count, generator := range setup {
		for range count {
			messages = append(messages, string(generator()))
		}
	}

	return messages
}

func generateEventMessage() []byte {
	event := generateRandomEvent()
	eventJSON, _ := json.Marshal(event)

	if rand.IntN(2) == 0 {
		subID := fmt.Sprintf("sub_%d", rand.IntN(1000))
		return []byte(fmt.Sprintf(`["EVENT","%s",%s]`, subID, string(eventJSON)))
	}

	return []byte(fmt.Sprintf(`["EVENT",%s]`, string(eventJSON)))
}

func generateRandomEvent() Event {
	tagCount := rand.IntN(10)
	tags := make(Tags, tagCount)
	for i := 0; i < tagCount; i++ {
		tagType := string([]byte{byte('a' + rand.IntN(26))})
		tagValues := make([]string, rand.IntN(3)+1)
		for j := range tagValues {
			tagValues[j] = fmt.Sprintf("%d", j)
		}
		tags[i] = append([]string{tagType}, tagValues...)
	}

	contentLength := rand.IntN(200) + 10
	content := make([]byte, contentLength)
	for i := range content {
		content[i] = byte('a' + rand.IntN(26))
	}

	event := Event{
		CreatedAt: Timestamp(time.Now().Unix() - int64(rand.IntN(10000000))),
		Kind:      Kind(rand.IntN(10000)),
		Tags:      tags,
		Content:   string(content),
	}
	event.ID, _ = IDFromHex(generateRandomHex(64))
	event.PubKey, _ = PubKeyFromHexCheap(generateRandomHex(64))

	return event
}

func generateNoticeMessage() []byte {
	noticeLength := rand.IntN(100) + 5
	notice := make([]byte, noticeLength)
	for i := range notice {
		notice[i] = byte('a' + rand.IntN(26))
	}

	return []byte(fmt.Sprintf(`["NOTICE","%s"]`, string(notice)))
}

func generateEOSEMessage() []byte {
	subID := fmt.Sprintf("sub_%d", rand.IntN(1000))
	return []byte(fmt.Sprintf(`["EOSE","%s"]`, subID))
}

func generateOKMessage() []byte {
	eventID := generateRandomHex(64)
	success := rand.IntN(2) == 0

	var reason string
	if !success {
		reasons := []string{
			"blocked",
			"rate-limited",
			"invalid: signature verification failed",
			"error: could not connect to the database",
			"pow: difficulty too low",
		}
		reason = reasons[rand.IntN(len(reasons))]
	}

	return []byte(fmt.Sprintf(`["OK","%s",%t,"%s"]`, eventID, success, reason))
}

func generateCountMessage() []byte {
	subID := fmt.Sprintf("sub_%d", rand.IntN(1000))
	count := rand.IntN(10000)

	if rand.IntN(5) == 0 {
		hll := generateRandomHex(512)
		return []byte(fmt.Sprintf(`["COUNT","%s",{"count":%d,"hll":"%s"}]`, subID, count, hll))
	}

	return []byte(fmt.Sprintf(`["COUNT","%s",{"count":%d}]`, subID, count))
}

func generateReqMessage() []byte {
	subID := fmt.Sprintf("sub_%d", rand.IntN(1000))

	filterCount := rand.IntN(3) + 1
	filters := make([]string, filterCount)

	for i := range filters {
		filter := generateRandomFilter()
		filterJSON, _ := json.Marshal(filter)
		filters[i] = string(filterJSON)
	}

	result := fmt.Sprintf(`["REQ","%s"`, subID)
	for _, f := range filters {
		result += "," + f
	}
	result += "]"

	return []byte(result)
}

func generateRandomFilter() Filter {
	filter := Filter{}

	if rand.IntN(2) == 0 {
		count := rand.IntN(5) + 1
		filter.IDs = make([]ID, count)
		for i := range filter.IDs {
			filter.IDs[i], _ = IDFromHex(generateRandomHex(64))
		}
	}

	if rand.IntN(2) == 0 {
		count := rand.IntN(5) + 1
		filter.Kinds = make([]Kind, count)
		for i := range filter.Kinds {
			filter.Kinds[i] = Kind(rand.IntN(10000))
		}
	}

	if rand.IntN(2) == 0 {
		count := rand.IntN(5) + 1
		filter.Authors = make([]PubKey, count)
		for i := range filter.Authors {
			filter.Authors[i], _ = PubKeyFromHexCheap(generateRandomHex(64))
		}
	}

	if rand.IntN(2) == 0 {
		tagCount := rand.IntN(3) + 1
		filter.Tags = make(TagMap)

		for i := 0; i < tagCount; i++ {
			tagName := string([]byte{byte('a' + rand.IntN(26))})
			valueCount := rand.IntN(3) + 1
			values := make([]string, valueCount)

			for j := range values {
				values[j] = fmt.Sprintf("tag_value_%d", rand.IntN(100))
			}

			filter.Tags[tagName] = values
		}
	}

	if rand.IntN(2) == 0 {
		filter.Since = Timestamp(time.Now().Unix() - int64(rand.IntN(10000000)))
	}

	if rand.IntN(2) == 0 {
		filter.Until = Timestamp(time.Now().Unix() - int64(rand.IntN(1000000)))
	}

	if rand.IntN(2) == 0 {
		filter.Limit = rand.IntN(100) + 1
	}

	return filter
}

func generateRandomHex(length int) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, length)

	for i := range result {
		result[i] = hexChars[rand.IntN(len(hexChars))]
	}

	return string(result)
}
