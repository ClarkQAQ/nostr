package gitnaturalapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type InfoRefsUploadPackResponse struct {
	Refs         map[string]string
	Capabilities []string
	Symrefs      map[string]string
}

var capabilitiesCache sync.Map

func GetCapabilities(url string, existingInfo *InfoRefsUploadPackResponse) ([]string, error) {
	if existingInfo != nil {
		capabilitiesCache.Store(url, existingInfo.Capabilities)
		return existingInfo.Capabilities, nil
	}

	if cached, ok := capabilitiesCache.Load(url); ok {
		return cached.([]string), nil
	}

	info, err := GetInfoRefs(url)
	if err != nil {
		return nil, err
	}
	capabilitiesCache.Store(url, info.Capabilities)
	return info.Capabilities, nil
}

func GetInfoRefs(url string) (*InfoRefsUploadPackResponse, error) {
	resp, err := http.Get(url + "/info/refs?service=git-upload-pack")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch info/refs: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read info/refs response: %w", err)
	}
	response := string(body)

	result := &InfoRefsUploadPackResponse{
		Refs:    make(map[string]string),
		Symrefs: make(map[string]string),
	}

	lines := strings.Split(response, "\n")
	firstRef := true
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		if strings.HasPrefix(line, "0000") {
			line = line[4:]
		}
		if len(line) < 4 {
			continue
		}

		length, err := strconv.ParseInt(line[:4], 16, 32)
		if err != nil {
			continue
		}

		endIdx := int(length)
		if endIdx > len(line) {
			endIdx = len(line)
		}
		if endIdx <= 4 {
			continue
		}
		content := line[4:endIdx]

		if firstRef && strings.HasPrefix(content, "# service=") {
			firstRef = false
			continue
		}

		if !strings.Contains(content, " ") {
			continue
		}

		parts := strings.SplitN(content, " ", 2)
		hash := parts[0]
		refAndCaps := parts[1]

		if strings.Contains(refAndCaps, "\x00") {
			nulParts := strings.SplitN(refAndCaps, "\x00", 2)
			ref := strings.TrimSpace(nulParts[0])
			result.Refs[ref] = hash

			caps := strings.Fields(nulParts[1])
			result.Capabilities = caps

			for _, cap := range caps {
				if strings.HasPrefix(cap, "symref=") {
					symrefData := cap[7:]
					colonIdx := strings.Index(symrefData, ":")
					if colonIdx != -1 {
						result.Symrefs[symrefData[:colonIdx]] = symrefData[colonIdx+1:]
					}
				}
			}
		} else {
			result.Refs[strings.TrimSpace(refAndCaps)] = hash
		}
	}

	return result, nil
}
