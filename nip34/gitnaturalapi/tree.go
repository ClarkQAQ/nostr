package gitnaturalapi

import "encoding/hex"

type TreeEntry struct {
	Path  string
	Mode  string
	IsDir bool
	Hash  string
}

type TreeFile struct {
	Name    string
	Hash    string
	Content []byte
}

type TreeDirectory struct {
	Name    string
	Hash    string
	Content *Tree
}

type Tree struct {
	Directories []TreeDirectory
	Files       []TreeFile
}

func LoadTree(obj *ParsedObject, objects map[string]*ParsedObject, depth *int) *Tree {
	directories := make([]TreeDirectory, 0)
	files := make([]TreeFile, 0)
	entries := ParseTree(obj.Data)

	for _, entry := range entries {
		child := objects[entry.Hash]

		if entry.IsDir {
			var content *Tree
			if child != nil && (depth == nil || *depth > 0) {
				var newDepth *int
				if depth != nil {
					d := *depth - 1
					newDepth = &d
				}
				content = LoadTree(child, objects, newDepth)
			}
			directories = append(directories, TreeDirectory{
				Name:    entry.Path,
				Hash:    entry.Hash,
				Content: content,
			})
		} else {
			var content []byte
			if child != nil {
				content = child.Data
			}
			files = append(files, TreeFile{
				Name:    entry.Path,
				Hash:    entry.Hash,
				Content: content,
			})
		}
	}

	return &Tree{Directories: directories, Files: files}
}

func ParseTree(treeData []byte) []TreeEntry {
	entries := make([]TreeEntry, 0)
	offset := 0

	for offset < len(treeData) {
		modeEnd := offset
		for treeData[modeEnd] != 0x20 {
			modeEnd++
		}
		mode := string(treeData[offset:modeEnd])
		offset = modeEnd + 1

		filenameEnd := offset
		for treeData[filenameEnd] != 0x00 {
			filenameEnd++
		}
		path := string(treeData[offset:filenameEnd])
		offset = filenameEnd + 1

		hash := hex.EncodeToString(treeData[offset : offset+20])
		offset += 20

		isDir := mode == "40000" || mode == "040000"

		entries = append(entries, TreeEntry{
			Mode:  mode,
			Path:  path,
			Hash:  hash,
			IsDir: isDir,
		})
	}

	return entries
}
