package gitnaturalapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

var NecessaryCapabilities = []string{
	"multi_ack_detailed",
	"side-band-64k",
}

var RequiredCapabilities = []string{
	"shallow",
	"object-format=sha1",
}

var DefaultCapabilities = []string{
	"ofs-delta",
	"no-progress",
}

type MissingRef struct{}

func (e *MissingRef) Error() string { return "missing ref" }

type InvalidCommit struct {
	Commit string
}

func (e *InvalidCommit) Error() string {
	return fmt.Sprintf("invalid commit '%s', must be 20 byte hex", e.Commit)
}

func FetchPackfile(url string, want string) (*PackfileResult, error) {
	req, err := http.NewRequest("POST", url+"/git-upload-pack", strings.NewReader(want))
	if err != nil {
		return nil, fmt.Errorf("failed to create git-upload-pack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Accept", "application/x-git-upload-pack-result")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call git-upload-pack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to call git-upload-pack: %s", string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read git-upload-pack response: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	offset := 0
	for offset < len(data) {
		prev := offset
		if prev+1 >= len(data) {
			break
		}
		nlIdx := bytes.IndexByte(data[prev+1:], '\n')
		if nlIdx == -1 {
			if len(data) >= 32 && string(data[4:32]) == "ERR upload-pack: not our ref" {
				return nil, &MissingRef{}
			}
			end := len(data)
			if end > 63 {
				end = 63
			}
			return nil, fmt.Errorf("unexpected '%s'", string(data[:end]))
		}
		offset = prev + nlIdx + 1
		if offset >= 3 && string(data[offset-3:offset]) == "NAK" {
			break
		}
	}
	offset++

	var packfileData []byte
	for offset < len(data) {
		if offset+5 > len(data) {
			break
		}
		pktLen, err := strconv.ParseInt(string(data[offset:offset+4]), 16, 32)
		if err != nil {
			break
		}
		length := int(pktLen)
		if length == 0 {
			break
		}
		if offset+length > len(data) {
			break
		}
		if data[offset+4] == 2 {
			// progress message, ignore
		} else if data[offset+4] == 1 {
			packfileData = append(packfileData, data[offset+5:offset+length]...)
		}
		offset += length
	}

	if len(packfileData) == 0 {
		return nil, &MissingRef{}
	}

	return ParsePackfile(packfileData)
}

func CreateWantRequest(commitSha string, capabilities []string, deepen *int, filter string) (string, error) {
	if len(commitSha) != 40 {
		return "", &InvalidCommit{Commit: commitSha}
	}

	var buf strings.Builder

	wantLine := fmt.Sprintf("want %s %s agent=nsa/1.0.0\n", commitSha, strings.Join(capabilities, " "))
	buf.WriteString(pktEncode(wantLine))

	if deepen != nil {
		deepenLine := fmt.Sprintf("deepen %d\n", *deepen)
		buf.WriteString(pktEncode(deepenLine))
	}

	if filter != "" {
		filterLine := fmt.Sprintf("filter %s\n", filter)
		buf.WriteString(pktEncode(filterLine))
	}

	buf.WriteString("0000")
	buf.WriteString(pktEncode("done\n"))

	return buf.String(), nil
}

func pktEncode(data string) string {
	if len(data) == 0 {
		return "0000"
	}
	length := len(data) + 4
	return fmt.Sprintf("%04x%s", length, data)
}
