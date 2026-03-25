package gitnaturalapi

import (
	"fmt"
	"slices"
	"strings"
)

type MissingCapability struct {
	URL        string
	Capability string
}

func (e *MissingCapability) Error() string {
	return fmt.Sprintf("server at %s is missing required capability %s", e.URL, e.Capability)
}

func prepareRequest(url string, commitOrRef string, needFilter bool) (resolvedRef string, capabilities []string, err error) {
	var info *InfoRefsUploadPackResponse
	if strings.HasPrefix(commitOrRef, "refs/") {
		info, err = GetInfoRefs(url)
		if err != nil {
			return "", nil, err
		}
		resolved, ok := info.Refs[commitOrRef]
		if !ok {
			return "", nil, fmt.Errorf("ref %s not found", commitOrRef)
		}
		commitOrRef = resolved
	}

	caps, err := GetCapabilities(url, info)
	if err != nil {
		return "", nil, err
	}

	for _, c := range DefaultCapabilities {
		if slices.Contains(caps, c) {
			capabilities = append(capabilities, c)
		}
	}
	for _, c := range NecessaryCapabilities {
		if slices.Contains(caps, c) {
			capabilities = append(capabilities, c)
		} else {
			return "", nil, &MissingCapability{URL: url, Capability: c}
		}
	}
	for _, c := range RequiredCapabilities {
		if !slices.Contains(caps, c) {
			return "", nil, &MissingCapability{URL: url, Capability: c}
		}
	}
	if needFilter {
		if slices.Contains(caps, "filter") {
			capabilities = append(capabilities, "filter")
		} else {
			return "", nil, &MissingCapability{URL: url, Capability: "filter"}
		}
	}

	return commitOrRef, capabilities, nil
}

func GetObject(url string, blobHash string) (*ParsedObject, error) {
	ref, caps, err := prepareRequest(url, blobHash, false)
	if err != nil {
		return nil, err
	}

	deepen := 1
	want, err := CreateWantRequest(ref, caps, &deepen, "")
	if err != nil {
		return nil, err
	}

	result, err := FetchPackfile(url, want)
	if err != nil {
		return nil, err
	}

	return result.Objects[blobHash], nil
}

func GetDirectoryTreeAt(url string, commitOrRef string, nestLimit *int) (*Tree, error) {
	ref, caps, err := prepareRequest(url, commitOrRef, true)
	if err != nil {
		return nil, err
	}

	want, err := CreateWantRequest(ref, caps, nestLimit, "blob:none")
	if err != nil {
		return nil, err
	}

	result, err := FetchPackfile(url, want)
	if err != nil {
		return nil, err
	}

	commit := result.Objects[ref]
	if commit == nil {
		return nil, fmt.Errorf("commit %s not found in packfile", ref)
	}

	treeHash := string(commit.Data[5:45])
	rootTree := result.Objects[treeHash]
	if rootTree == nil {
		return nil, fmt.Errorf("root tree %s not found in packfile", treeHash)
	}

	return LoadTree(rootTree, result.Objects, nestLimit), nil
}

func ShallowCloneRepositoryAt(url string, commitOrRef string) (*Commit, *Tree, error) {
	ref, caps, err := prepareRequest(url, commitOrRef, false)
	if err != nil {
		return nil, nil, err
	}

	deepen := 1
	want, err := CreateWantRequest(ref, caps, &deepen, "")
	if err != nil {
		return nil, nil, err
	}

	result, err := FetchPackfile(url, want)
	if err != nil {
		return nil, nil, err
	}

	commitObj := result.Objects[ref]
	if commitObj == nil {
		return nil, nil, fmt.Errorf("commit %s not found in packfile", ref)
	}

	treeHash := string(commitObj.Data[5:45])
	rootTree := result.Objects[treeHash]
	if rootTree == nil {
		return nil, nil, fmt.Errorf("root tree %s not found in packfile", treeHash)
	}

	commit, err := ParseCommit(commitObj.Data, commitObj.Hash)
	if err != nil {
		return nil, nil, err
	}

	tree := LoadTree(rootTree, result.Objects, nil)
	return commit, tree, nil
}

func FetchCommitsOnly(url string, commitOrRef string, maxCommits *int) ([]*Commit, error) {
	ref, caps, err := prepareRequest(url, commitOrRef, true)
	if err != nil {
		return nil, err
	}

	want, err := CreateWantRequest(ref, caps, maxCommits, "tree:0")
	if err != nil {
		return nil, err
	}

	result, err := FetchPackfile(url, want)
	if err != nil {
		return nil, err
	}

	commitMap := make(map[string]*Commit, len(result.Objects))
	for hash, obj := range result.Objects {
		commit, err := ParseCommit(obj.Data, hash)
		if err != nil {
			return nil, err
		}
		commitMap[hash] = commit
	}

	// sort topologically starting from the requested ref
	sorted := make([]*Commit, 0, len(commitMap))
	visited := make(map[string]bool, len(commitMap))
	var visit func(hash string)
	visit = func(hash string) {
		if visited[hash] {
			return
		}
		c, ok := commitMap[hash]
		if !ok {
			return
		}
		visited[hash] = true
		sorted = append(sorted, c)
		for _, parent := range c.Parents {
			visit(parent)
		}
	}
	visit(ref)

	for _, c := range commitMap {
		if !visited[c.Hash] {
			sorted = append(sorted, c)
		}
	}

	return sorted, nil
}

func GetSingleCommit(url string, commitOrRef string) (*Commit, error) {
	maxCommits := 1
	commits, err := FetchCommitsOnly(url, commitOrRef, &maxCommits)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("no commit found for reference: %s", commitOrRef)
	}
	return commits[0], nil
}

func GetObjectByPath(url string, commitOrRef string, path string) (*TreeEntry, error) {
	normalizedPath := strings.ReplaceAll(path, "\\", "/")
	normalizedPath = strings.TrimLeft(normalizedPath, "/")
	normalizedPath = strings.TrimRight(normalizedPath, "/")

	var pathSegments []string
	if normalizedPath != "" {
		pathSegments = strings.Split(normalizedPath, "/")
	}
	requiredDepth := len(pathSegments)

	tree, err := GetDirectoryTreeAt(url, commitOrRef, &requiredDepth)
	if err != nil {
		return nil, err
	}

	currentLevel := tree
nextSegment:
	for i, segment := range pathSegments {
		isLastSegment := i == len(pathSegments)-1

		for _, dir := range currentLevel.Directories {
			if dir.Name == segment {
				if isLastSegment {
					return &TreeEntry{Path: segment, Mode: "40000", IsDir: true, Hash: dir.Hash}, nil
				}
				if dir.Content != nil {
					currentLevel = dir.Content
					continue nextSegment
				}
				return nil, nil
			}
		}

		if isLastSegment {
			for _, file := range currentLevel.Files {
				if file.Name == segment {
					return &TreeEntry{Path: segment, Mode: "100644", IsDir: false, Hash: file.Hash}, nil
				}
			}
		}

		return nil, nil
	}

	return nil, nil
}
