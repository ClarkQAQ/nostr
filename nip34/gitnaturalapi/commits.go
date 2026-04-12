package gitnaturalapi

import (
	"fmt"
	"strconv"
	"strings"
)

type Person struct {
	Name      string
	Email     string
	Timestamp int64
	Timezone  string
}

type Commit struct {
	Hash      string
	Tree      string
	Parents   []string
	Author    Person
	Committer Person
	Message   string
}

func ParseCommit(data []byte, hash string) (*Commit, error) {
	content := string(data)

	headerEndIndex := strings.Index(content, "\n\n")
	if headerEndIndex == -1 {
		return nil, fmt.Errorf("invalid commit format for %s: no message separator found", hash)
	}

	header := content[:headerEndIndex]
	message := content[headerEndIndex+2:]

	lines := strings.Split(header, "\n")
	result := &Commit{
		Hash:    hash,
		Parents: []string{},
		Message: message,
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "tree ") {
			result.Tree = line[5:]
		} else if strings.HasPrefix(line, "parent ") {
			result.Parents = append(result.Parents, line[7:])
		} else if strings.HasPrefix(line, "author ") {
			person, err := parsePerson(line[7:])
			if err != nil {
				return nil, fmt.Errorf("invalid author in commit %s: %w", hash, err)
			}
			result.Author = person
		} else if strings.HasPrefix(line, "committer ") {
			person, err := parsePerson(line[10:])
			if err != nil {
				return nil, fmt.Errorf("invalid committer in commit %s: %w", hash, err)
			}
			result.Committer = person
		}
	}

	if result.Tree == "" {
		return nil, fmt.Errorf("invalid commit format for %s: missing tree", hash)
	}
	if result.Author.Name == "" {
		return nil, fmt.Errorf("invalid commit format for %s: missing author", hash)
	}
	if result.Committer.Name == "" {
		return nil, fmt.Errorf("invalid commit format for %s: missing committer", hash)
	}

	return result, nil
}

func parsePerson(line string) (Person, error) {
	emailStart := strings.Index(line, " <")
	if emailStart == -1 {
		return Person{}, fmt.Errorf("invalid person format: %s", line)
	}
	name := line[:emailStart]

	emailEnd := strings.Index(line[emailStart+2:], ">")
	if emailEnd == -1 {
		return Person{}, fmt.Errorf("invalid person format: %s", line)
	}
	email := line[emailStart+2 : emailStart+2+emailEnd]

	rest := strings.TrimSpace(line[emailStart+2+emailEnd+1:])
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) != 2 {
		return Person{}, fmt.Errorf("invalid person format: %s", line)
	}

	timestamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Person{}, fmt.Errorf("invalid timestamp in person: %s", parts[0])
	}

	return Person{
		Name:      name,
		Email:     email,
		Timestamp: timestamp,
		Timezone:  parts[1],
	}, nil
}
