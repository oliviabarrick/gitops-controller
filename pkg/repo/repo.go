package repo

import (
	"fmt"
	"github.com/justinbarrick/git-controller/pkg/util"
	"github.com/justinbarrick/git-controller/pkg/yaml"
	"gopkg.in/src-d/go-git.v4"

	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/go-git.v4/storage/memory"
	"gopkg.in/src-d/go-billy.v4/memfs"
	"gopkg.in/src-d/go-billy.v4"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Object for manipulating git repositories.
type Repo struct {
	fs      billy.Filesystem
	repo    *git.Repository
	tree    *git.Worktree
	lock    sync.Mutex
	workDir string
	repoDir string
}

// Open a git repository.
func NewRepo(repoDir, workDir string) (*Repo, error) {
	fs := memfs.New()

	util.Log.Info("cloning repo", "repo", repoDir)
	startTime := time.Now()
	repo, err := git.Clone(memory.NewStorage(), fs, &git.CloneOptions{
		URL: repoDir,
	})
	if err != nil {
		return nil, err
	}
	duration := time.Now().Sub(startTime).Seconds()
	util.Log.Info("finished clone", "repo", repoDir, "duration", duration)

	tree, err := repo.Worktree()
	if err != nil {
		return nil, err
	}

	if workDir == "" {
		workDir = "."
	}

	return &Repo{
		fs:      fs,
		repo:    repo,
		tree:    tree,
		repoDir: repoDir,
		workDir: workDir,
	}, nil
}

// Return true if the repository has had changes since the last time it was commited
// to.
func (r *Repo) IsClean() (bool, error) {
	status, err := r.tree.Status()
	if err != nil {
		return false, err
	}

	return status.IsClean(), nil
}

// Commit staged changes to git, does nothing if there are no changes.
func (r *Repo) Commit(message string) error {
	clean, err := r.IsClean()
	if err != nil {
		return err
	}

	// nothing to do
	if clean {
		return nil
	}

	commitId, err := r.tree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	util.Log.Info("commited", "commit", fmt.Sprintf("%x", commitId))
	return nil
}

// Add a file to the repository.
func (r *Repo) Add(path string) error {
	_, err := r.tree.Add(path)
	return err
}

func (r *Repo) Walk(path string, cb func(string, os.FileInfo) error) error {
	files, err := r.fs.ReadDir(path)
	if err != nil {
		return err
	}

	for _, file := range files {
		fullPath := filepath.Join(path, file.Name())

		if file.IsDir() {
			err = r.Walk(fullPath, cb)
		} else {
			err = cb(fullPath, file)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// Load all YAML files in a repository.
func (r *Repo) LoadRepoYAMLs() ([]*yaml.Object, error) {
	mappings := []*yaml.Object{}

	allowedExtensions := map[string]bool{
		".yaml": true,
		".yml": true,
		".json": true,
	}

	return mappings, r.Walk(r.workDir, func(path string, info os.FileInfo) error {
		if ! allowedExtensions[filepath.Ext(path)] {
			return nil
		}

		file := yaml.NewFile(r.fs, path)

		objects, err := file.Load()
		if err != nil {
			return err
		}

		mappings = append(mappings, objects...)
		return nil
	})
}

// Search the repository for any files that have a matching object, returning a
// yaml.Object. Returns nil if the object is not found in the repository.
func (r *Repo) FindObjectInRepo(obj runtime.Object) (*yaml.Object, error) {
	var found *yaml.Object

	objectMappings, err := r.LoadRepoYAMLs()
	if err != nil {
		return found, err
	}

	for _, objMapping := range objectMappings {
		if !objMapping.Matches(obj) {
			continue
		}

		found = objMapping
		break
	}

	return found, nil
}

// Add an object to a repository - if it exists in the repository already, update
// it in place, if not, create a new file and write it to that file.
func (r *Repo) AddResource(obj runtime.Object) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	found, err := r.FindObjectInRepo(obj)
	if err != nil {
		return err
	}

	if found == nil {
		meta := util.GetMeta(obj)
		kind := util.GetType(obj)

		fname := fmt.Sprintf("%s.yaml", meta.GetName())
		gitPath := filepath.Join(meta.GetNamespace(), kind.Kind, fname)

		found = &yaml.Object{}

		file := yaml.NewFile(r.fs, gitPath)
		file.AddResource(found)
	}

	found.SetObject(obj)
	if err := found.Save(); err != nil {
		return err
	}

	if err := r.Add(r.RelativePath(found.File.Path)); err != nil {
		return err
	}

	return r.Commit("Adding resource")
}

// Remove an object from the repository if it exists.
func (r *Repo) RemoveResource(obj runtime.Object) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	found, err := r.FindObjectInRepo(obj)
	if err != nil {
		return err
	}

	if found == nil {
		return nil
	}

	path := found.File.Path

	if err := found.Delete(); err != nil {
		return err
	}

	if err := r.Add(r.RelativePath(path)); err != nil {
		return err
	}

	return r.Commit("Removing resource")
}

// Return a path relative to the git repository root.
func (r *Repo) RelativePath(path string) string {
	rel, _ := filepath.Rel(r.repoDir, path)
	return rel
}
