package gitnaturalapi

import (
	"strings"
	"sync"
)

type DiffLine struct {
	Index  int
	Status string
	Text   string
	Change string
}

type DiffFile struct {
	Path    string
	Status  string
	Content []byte
	Lines   []DiffLine
}

type changedEntry struct {
	newVersion  TreeEntry
	oldVersions []TreeEntry
}

func GetCommitDiff(url string, commitOrRef string) ([]DiffFile, error) {
	commit, err := GetSingleCommit(url, commitOrRef)
	if err != nil {
		return nil, err
	}

	added := make(map[string]TreeEntry)
	deleted := make(map[string]TreeEntry)
	changed := make(map[string]*changedEntry)
	unchanged := make(map[string]bool)

	for _, parent := range commit.Parents {
		parentCommit, err := GetSingleCommit(url, parent)
		if err != nil {
			return nil, err
		}
		err = computeTreeDiffs(url, commit.Tree, parentCommit.Tree, "", added, deleted, changed, unchanged)
		if err != nil {
			return nil, err
		}
	}

	var diff []DiffFile
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for path, entry := range changed {
		p := path
		e := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			curr, err := GetObject(url, e.newVersion.Hash)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if curr == nil {
				return
			}

			if len(e.oldVersions) == 0 {
				return
			}

			oldObj, err := GetObject(url, e.oldVersions[0].Hash)
			if err != nil || oldObj == nil {
				return
			}

			if isBinary(curr.Data) || isBinary(oldObj.Data) {
				mu.Lock()
				diff = append(diff, DiffFile{
					Path:   p,
					Status: "changed-binary",
				})
				mu.Unlock()
				return
			}

			lines := diffTextLines(oldObj.Data, curr.Data)
			mu.Lock()
			diff = append(diff, DiffFile{
				Path:   p,
				Status: "changed",
				Lines:  lines,
			})
			mu.Unlock()
		}()
	}

	for path, entry := range deleted {
		p := path
		e := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			obj, err := GetObject(url, e.Hash)
			if err != nil || obj == nil {
				return
			}
			mu.Lock()
			diff = append(diff, DiffFile{
				Path:    p,
				Status:  "deleted",
				Content: obj.Data,
			})
			mu.Unlock()
		}()
	}

	for path, entry := range added {
		p := path
		e := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			obj, err := GetObject(url, e.Hash)
			if err != nil || obj == nil {
				return
			}
			mu.Lock()
			diff = append(diff, DiffFile{
				Path:    p,
				Status:  "added",
				Content: obj.Data,
			})
			mu.Unlock()
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return diff, nil
}

func isBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func diffTextLines(oldData []byte, newData []byte) []DiffLine {
	oldText := string(oldData)
	newText := string(newData)
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	ops := lcsOperations(oldLines, newLines)
	allLines := make([]DiffLine, 0, len(ops))
	oldIndex := 1
	newIndex := 1

	for i := 0; i < len(ops); i++ {
		op := ops[i]
		var next *lcsOp
		if i+1 < len(ops) {
			next = &ops[i+1]
		}

		if op.typ == "del" && next != nil && next.typ == "add" {
			allLines = append(allLines, DiffLine{
				Status: "changed",
				Index:  newIndex,
				Text:   next.line,
			})
			oldIndex++
			newIndex++
			i++
			continue
		}

		if op.typ == "add" && next != nil && next.typ == "del" {
			allLines = append(allLines, DiffLine{
				Status: "changed",
				Index:  newIndex,
				Text:   op.line,
			})
			oldIndex++
			newIndex++
			i++
			continue
		}

		if op.typ == "add" {
			allLines = append(allLines, DiffLine{
				Status: "added",
				Index:  newIndex,
				Text:   op.line,
			})
			newIndex++
			continue
		}

		if op.typ == "del" {
			allLines = append(allLines, DiffLine{
				Status: "deleted",
				Index:  oldIndex,
				Text:   op.line,
			})
			oldIndex++
			continue
		}

		oldIndex++
		newIndex++
	}

	if len(allLines) == 0 {
		return allLines
	}

	keep := make([]bool, len(allLines))
	for i := 0; i < len(allLines); i++ {
		start := i - 3
		if start < 0 {
			start = 0
		}
		end := i + 3
		if end >= len(allLines) {
			end = len(allLines) - 1
		}
		for j := start; j <= end; j++ {
			keep[j] = true
		}
	}

	result := make([]DiffLine, 0, len(allLines))
	for i := 0; i < len(allLines); i++ {
		if keep[i] {
			result = append(result, allLines[i])
		}
	}

	return result
}

type lcsOp struct {
	typ  string
	line string
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lcsOperations(oldLines []string, newLines []string) []lcsOp {
	n := len(oldLines)
	m := len(newLines)

	dp := make([][]uint32, n+1)
	for i := range dp {
		dp[i] = make([]uint32, m+1)
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	ops := make([]lcsOp, 0, n+m)
	i := n
	j := m
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			ops = append(ops, lcsOp{typ: "equal", line: oldLines[i-1]})
			i--
			j--
			continue
		}

		if i > 0 && (j == 0 || dp[i-1][j] >= dp[i][j-1]) {
			ops = append(ops, lcsOp{typ: "del", line: oldLines[i-1]})
			i--
			continue
		}

		if j > 0 {
			ops = append(ops, lcsOp{typ: "add", line: newLines[j-1]})
			j--
		}
	}

	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}

	return ops
}

func computeTreeDiffs(
	url string,
	treeHash string,
	parentTreeHash string,
	basePath string,
	added map[string]TreeEntry,
	deleted map[string]TreeEntry,
	changed map[string]*changedEntry,
	unchanged map[string]bool,
) error {
	var newTree []TreeEntry
	var oldTree []TreeEntry

	if treeHash != "" {
		obj, err := GetObject(url, treeHash)
		if err != nil {
			return err
		}
		if obj != nil {
			newTree = ParseTree(obj.Data)
		}
	}

	if parentTreeHash != "" {
		obj, err := GetObject(url, parentTreeHash)
		if err != nil {
			return err
		}
		if obj != nil {
			oldTree = ParseTree(obj.Data)
		}
	}

	for _, entry := range newTree {
		var old *TreeEntry
		for _, o := range oldTree {
			if o.Path == entry.Path {
				o := o
				old = &o
				break
			}
		}

		if old != nil {
			delete(added, basePath+entry.Path)

			if old.Hash == entry.Hash {
				unchanged[basePath+entry.Path] = true
			} else {
				if entry.IsDir {
					err := computeTreeDiffs(url, entry.Hash, old.Hash, basePath+entry.Path+"/", added, deleted, changed, unchanged)
					if err != nil {
						return err
					}
				} else {
					if existing, exists := changed[basePath+entry.Path]; !exists {
						changed[basePath+entry.Path] = &changedEntry{
							newVersion:  entry,
							oldVersions: []TreeEntry{*old},
						}
					} else {
						existing.oldVersions = append(existing.oldVersions, *old)
					}
				}
			}
		} else {
			if entry.IsDir {
				err := computeTreeDiffs(url, entry.Hash, "", basePath+entry.Path+"/", added, deleted, changed, unchanged)
				if err != nil {
					return err
				}
			} else {
				added[basePath+entry.Path] = entry
			}
		}
	}

	for _, old := range oldTree {
		if unchanged[basePath+old.Path] || changed[basePath+old.Path] != nil {
			continue
		}

		if old.IsDir {
			err := computeTreeDiffs(url, "", old.Hash, basePath+old.Path+"/", added, deleted, changed, unchanged)
			if err != nil {
				return err
			}
		} else {
			deleted[basePath+old.Path] = old
		}
	}

	return nil
}
