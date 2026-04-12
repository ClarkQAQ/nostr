package gitnaturalapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetRefs(t *testing.T) {
	info, err := GetInfoRefs("https://codeberg.org/dluvian/gitplaza.git")
	require.NoError(t, err)

	require.Contains(t, info.Capabilities, "shallow")
	require.Contains(t, info.Capabilities, "object-format=sha1")
	require.Greater(t, len(info.Refs), 5)
	require.Contains(t, info.Refs, "refs/heads/master")
	require.Equal(t, "a04d0761564b0d23c5edbadf494ab4f1cc4656f4", info.Refs["refs/tags/v0.1.0"])
	require.Equal(t, "refs/heads/master", info.Symrefs["HEAD"])
}

func TestGetOnlyTreeAtCurrentCommit(t *testing.T) {
	urls := []string{
		"https://codeberg.org/dluvian/gitplaza.git",
		"https://github.com/fiatjaf/pyramid.git",
		"https://pyramid.fiatjaf.com/npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6/nostrlib.git",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			tree, err := GetDirectoryTreeAt(url, "refs/heads/master", nil)
			require.NoError(t, err)
			for _, file := range tree.Files {
				require.Nil(t, file.Content, "file %q should have nil content at %s", file.Name, url)
			}
			require.Greater(t, len(tree.Directories), 2, "at %s", url)
		})
	}
}

func TestCloneRepositoryAtCurrentCommit(t *testing.T) {
	urls := []string{
		"https://codeberg.org/dluvian/gitplaza.git",
		"https://github.com/fiatjaf/pyramid.git",
		"https://pyramid.fiatjaf.com/npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6/nostrlib.git",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			commit, tree, err := ShallowCloneRepositoryAt(url, "refs/heads/master")
			require.NoError(t, err)
			require.Greater(t, len(tree.Files), 5, "at %s", url)
			require.Greater(t, len(tree.Directories), 2, "at %s", url)

			info, err := GetInfoRefs(url)
			require.NoError(t, err)
			require.Equal(t, info.Refs["refs/heads/master"], commit.Hash, "at %s", url)
		})
	}
}

func TestGetSpecificObject(t *testing.T) {
	url := "https://codeberg.org/dluvian/gitplaza.git"
	hash := "0f9438a8fd68594cd663fb8dbd23c5f5139f5263" // shell.nix

	blob, err := GetObject(url, hash)
	require.NoError(t, err)
	require.NotNil(t, blob)
	require.Equal(t, ObjectTypeBlob, blob.Type)

	expected := "(builtins.getFlake\n  (\"git+file://\" + toString ./.)).devShells.${builtins.currentSystem}.default\n"
	require.Equal(t, expected, string(blob.Data))
}

func TestGetNonExistentCommit(t *testing.T) {
	urls := []string{
		"https://codeberg.org/dluvian/gitplaza.git",
		"https://pyramid.fiatjaf.com/npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6/nostrlib.git",
		"https://github.com/fiatjaf/nak.git",
	}

	commit := "1d4438a8fd68594cd663fb8dbd23c5f5139fabcd" // doesn't exist

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			_, err := GetDirectoryTreeAt(url, commit, nil)
			require.Error(t, err)
			var missingRef *MissingRef
			require.ErrorAs(t, err, &missingRef)
		})
	}
}

func TestFetchListOfCommits(t *testing.T) {
	commits, err := FetchCommitsOnly(
		"https://pyramid.fiatjaf.com/npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6/nostrlib.git",
		"refs/heads/master",
		nil,
	)
	require.NoError(t, err)
	require.Greater(t, len(commits), 10)
}

func TestFetch10PastCommits(t *testing.T) {
	maxCommits := 10
	commits, err := FetchCommitsOnly(
		"https://github.com/fiatjaf/pyramid.git",
		"57712756e37d7c60d1ac53e0f6b59e9ecad67c9a",
		&maxCommits,
	)
	require.NoError(t, err)
	require.Len(t, commits, 10)

	c := commits[1]
	require.Equal(t, "49c1b48f5120bad4089535a190d2233c96188fa2", c.Hash)
	require.Equal(t, "286786a6f1072a2ef5ae057fbb611858b8e88bc4", c.Tree)
	require.Equal(t, []string{"1599e46c0ee6f460e25048880754868d4f9644fd"}, c.Parents)
	require.Equal(t, "fiatjaf", c.Author.Name)
	require.Equal(t, "fiatjaf@gmail.com", c.Author.Email)
	require.Equal(t, int64(1767157644), c.Author.Timestamp)
	require.Equal(t, "-0300", c.Author.Timezone)
	require.Equal(t, "fiatjaf", c.Committer.Name)
	require.Equal(t, "fiatjaf@gmail.com", c.Committer.Email)
	require.Equal(t, int64(1767157724), c.Committer.Timestamp)
	require.Equal(t, "-0300", c.Committer.Timezone)
	require.Equal(t, "scheduled events.\n", c.Message)

	expectedMsg5 := "turn off groups logic on QueryStore and PreventBroadcast when groups is turned off.\n\nthis was causing crashes that Golang's bizarre iter API showed as happening inside SortedMerge.\n"
	require.Equal(t, expectedMsg5, commits[5].Message)
}

func TestGetSingleCommit(t *testing.T) {
	url := "https://github.com/fiatjaf/pyramid.git"
	commit, err := GetSingleCommit(url, "5e982dd1122a0bb1b0154c222ec4ba841f3820c6")
	require.NoError(t, err)

	require.Equal(t, "5e982dd1122a0bb1b0154c222ec4ba841f3820c6", commit.Hash)
	require.Equal(t, "fiatjaf", commit.Author.Name)
	require.Equal(t, "validate incoming git-related stuff.\n", commit.Message)
}

func TestGetDirectoryTreeWithDepthLimit(t *testing.T) {
	url := "https://github.com/fiatjaf/pyramid.git"

	fullTree, err := GetDirectoryTreeAt(url, "refs/heads/master", nil)
	require.NoError(t, err)

	depth := 1
	shallowTree, err := GetDirectoryTreeAt(url, "refs/heads/master", &depth)
	require.NoError(t, err)

	require.Equal(t, len(fullTree.Directories), len(shallowTree.Directories))

	for _, dir := range shallowTree.Directories {
		require.NotNil(t, dir.Content, "directory %q content should not be nil at depth 1", dir.Name)
		for _, file := range dir.Content.Files {
			require.NotEmpty(t, file.Name)
			require.Nil(t, file.Content, "file %q content should be nil", file.Name)
		}
		for _, subdir := range dir.Content.Directories {
			require.NotEmpty(t, subdir.Name)
			require.Nil(t, subdir.Content, "subdir %q content should be nil at depth 1", subdir.Name)
		}
	}

	require.Equal(t, len(fullTree.Files), len(shallowTree.Files))
}

func TestGetObjectByPathExistingFile(t *testing.T) {
	url := "https://codeberg.org/dluvian/gitplaza.git"
	entry, err := GetObjectByPath(url, "refs/heads/master", "README.md")
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, "README.md", entry.Path)
	require.False(t, entry.IsDir)
	require.Equal(t, "100644", entry.Mode)
	require.NotEmpty(t, entry.Hash)
}

func TestGetObjectByPathExistingDirectory(t *testing.T) {
	url := "https://codeberg.org/dluvian/gitplaza.git"
	entry, err := GetObjectByPath(url, "refs/heads/master", "src")
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, "src", entry.Path)
	require.True(t, entry.IsDir)
	require.Equal(t, "40000", entry.Mode)
	require.NotEmpty(t, entry.Hash)
}

func TestGetObjectByPathNestedFile(t *testing.T) {
	url := "https://github.com/fiatjaf/pyramid.git"
	entry, err := GetObjectByPath(url, "d567c18cd5c144a58b0214216f454b3caf49d4ff", "grasp/grasp.templ")
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, "grasp.templ", entry.Path)
	require.False(t, entry.IsDir)
	require.Equal(t, "100644", entry.Mode)
	require.Equal(t, "05bce14339ece5f48c670d0592faa8dece9e8957", entry.Hash)
}

func TestGetObjectByPathNonExistent(t *testing.T) {
	url := "https://codeberg.org/dluvian/gitplaza.git"
	entry, err := GetObjectByPath(url, "refs/heads/master", "whatever/something/x/y/z/non-existent-file.txt")
	require.NoError(t, err)
	require.Nil(t, entry)
}

func TestGetCommitDiff(t *testing.T) {
	url := "https://github.com/smallhelm/diff-lines.git"
	diff, err := GetCommitDiff(url, "a73592653fe9d01f948ca3035e088e45f722eca7")
	require.NoError(t, err)
	require.NotNil(t, diff)
	require.Len(t, diff, 5)

	byPath := make(map[string]DiffFile, len(diff))
	for _, f := range diff {
		byPath[f.Path] = f
	}

	for _, tc := range []struct {
		path   string
		status string
	}{
		{".travis.yml", "added"},
		{".gitignore", "added"},
		{"index.js", "added"},
		{"tests.js", "added"},
		{"package.json", "changed"},
	} {
		f, ok := byPath[tc.path]
		require.True(t, ok, "missing diff file %q", tc.path)
		require.Equal(t, tc.status, f.Status, "%s status", tc.path)
	}

	gitignore, ok := byPath[".gitignore"]
	require.True(t, ok, "missing .gitignore in diff")
	require.Equal(t, "/node_modules\n", string(gitignore.Content))

	pkg, ok := byPath["package.json"]
	require.True(t, ok, "missing package.json in diff")
	require.NotEmpty(t, pkg.Lines, "package.json should have diff lines")

	normalizeLineStatus := func(status string) string {
		if status == "same" {
			return "changed"
		}
		return status
	}

	lineByIndex := make(map[int]DiffLine, len(pkg.Lines))
	for _, line := range pkg.Lines {
		lineByIndex[line.Index] = line
	}

	expectedLines := []struct {
		Index  int
		Status string
		Text   string
	}{
		{Index: 22, Status: "added", Text: "  },"},
		{Index: 23, Status: "added", Text: "  \"homepage\": \"https://github.com/smallhelm/diff-lines#readme\","},
		{Index: 24, Status: "added", Text: "  \"devDependencies\": {"},
		{Index: 25, Status: "added", Text: "    \"tape\": \"^4.6.0\""},
		{Index: 27, Status: "added", Text: "  \"dependencies\": {"},
		{Index: 28, Status: "added", Text: "    \"diff\": \"^2.2.3\""},
		{Index: 29, Status: "changed", Text: "  }"},
	}

	actualLines := make([]struct {
		Index  int
		Status string
		Text   string
	}, 0, len(expectedLines))
	for _, expected := range expectedLines {
		line, ok := lineByIndex[expected.Index]
		require.True(t, ok, "missing package.json diff line %d", expected.Index)
		actualLines = append(actualLines, struct {
			Index  int
			Status string
			Text   string
		}{
			Index:  line.Index,
			Status: normalizeLineStatus(line.Status),
			Text:   line.Text,
		})
	}

	require.Equal(t, expectedLines, actualLines)
}
