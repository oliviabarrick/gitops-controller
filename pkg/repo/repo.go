package repo

import (
	"fmt"
	"github.com/justinbarrick/git-controller/pkg/util"
	"github.com/justinbarrick/git-controller/pkg/yaml"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"k8s.io/apimachinery/pkg/runtime"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Object for manipulating git repositories.
type Repo struct {
	repo    *git.Repository
	tree    *git.Worktree
	lock    sync.Mutex
	repoDir string
}

// Open a git repository.
func NewRepo(repoDir string) (*Repo, error) {
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		return nil, err
	}

	tree, err := repo.Worktree()
	if err != nil {
		return nil, err
	}

	return &Repo{
		repo:    repo,
		tree:    tree,
		repoDir: repoDir,
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

	log.Printf("Saved commit (%x)", commitId[:4])
	return nil
}

// Add a file to the repository.
func (r *Repo) Add(path string) error {
	_, err := r.tree.Add(path)
	return err
}

// Load all YAML files in a repository.
func (r *Repo) LoadRepoYAMLs() ([]*yaml.Object, error) {
	mappings := []*yaml.Object{}

	return mappings, filepath.Walk(r.repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(filepath.Join(r.repoDir, ".git"), path)
		if err != nil {
			return nil
		}

		if !strings.HasPrefix(rel, "../") {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		file := yaml.NewFile(path)

		err = file.Load()
		if err != nil {
			return err
		}

		mappings = append(mappings, file.Objects...)
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
		gitPath := filepath.Join(r.repoDir, meta.GetNamespace(), kind.Kind, fname)

		found = &yaml.Object{}

		file := yaml.NewFile(gitPath)
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
