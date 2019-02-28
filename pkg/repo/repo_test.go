package repo

import (
	"sort"
	"github.com/stretchr/testify/assert"
	"testing"
	"gopkg.in/src-d/go-billy.v4/osfs"
	//"gopkg.in/src-d/go-billy.v4/memfs"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/storage/filesystem"
	"gopkg.in/src-d/go-git.v4/plumbing/cache"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"os"
	"io/ioutil"
)

func doCommit(path, text string, r *Repo) (string, error) {
	file, err := r.fs.Create(path)
	if err != nil {
		return "", err
	}

	defer file.Close()

	if _, err := file.Write([]byte(text)); err != nil {
		return "", err
	}

	if err := r.Add(path); err != nil {
		return "", err
	}

	err = r.Commit("a commit")
	if err != nil {
		return "", err
	}

	ref, err := r.repo.Head()
	if err != nil {
		return "", err
	}

	return ref.Hash().String(), nil
}

func TestCommitIsAtomic(t *testing.T) {
	dir, err := ioutil.TempDir("", "gitops-test")
	assert.Nil(t, err)
	defer os.RemoveAll(dir)

	store := filesystem.NewStorage(osfs.New(dir), cache.NewObjectLRUDefault())
	repo, err := git.Init(store, nil)
	assert.Nil(t, err)

	expectedCommits := []string{}

	r2, err := NewRepo(dir, "", "")
	assert.Nil(t, err)

	commit, err := doCommit("hello.yaml", "new contents", r2)
	assert.Nil(t, err)
	expectedCommits = append(expectedCommits, commit)

	r3, err := NewRepo(dir, "", "")
	assert.Nil(t, err)

	commit, err = doCommit("hello.yaml", "other contents", r3)
	assert.Nil(t, err)
	expectedCommits = append(expectedCommits, commit)

	_, err = doCommit("hello.yaml", "my commit", r2)
	assert.NotNil(t, err)

	commit, err = doCommit("hello.yaml", "my commit", r2)
	assert.Nil(t, err)
	expectedCommits = append(expectedCommits, commit)

	ref, err := repo.Head()
	assert.Nil(t, err)

	headCommit, err := repo.CommitObject(ref.Hash())
	assert.Nil(t, err)

	commitIter, err := repo.Log(&git.LogOptions{From: headCommit.Hash})
	assert.Nil(t, err)

	commits := []string{}
	err = commitIter.ForEach(func(c *object.Commit) error {
		commits = append(commits, c.Hash.String())
		return nil
	})
	assert.Nil(t, err)

	sort.Strings(expectedCommits)
	sort.Strings(commits)
	assert.Equal(t, expectedCommits, commits)
}
