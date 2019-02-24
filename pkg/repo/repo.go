package repo

import (
	"github.com/justinbarrick/git-controller/pkg/yaml"
	"strings"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"path/filepath"
	"gopkg.in/src-d/go-git.v4"
	"os"
	"k8s.io/apimachinery/pkg/runtime"
)

type Repo struct {
	repo *git.Repository
	tree *git.Worktree
	repoDir string
}

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
		repo: repo,
		tree: tree,
		repoDir: repoDir,
	}, nil
}

func (r *Repo) IsClean() (bool, error) {
	status, err := r.tree.Status()
	if err != nil {
		return false, err
	}

	return status.IsClean(), nil
}

func (r *Repo) Commit(message string) error {
	clean, err := r.IsClean()
	if err != nil {
		return err
	}

	// nothing to do
	if clean {
		return nil
	}

	_, err = r.tree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name: "test",
			Email: "test@test.com",
		},
	})
	return err
}

func (r *Repo) Remove(path string) error {
	_, err := r.tree.Remove(path)
	return err
}

func (r *Repo) Add(path string) error {
	_, err := r.tree.Add(path)
	return err
}

func (r *Repo) LoadReposYAMLs() ([]*yaml.ObjectMapping, error) {
	mappings := []*yaml.ObjectMapping{}

	return mappings, filepath.Walk("/tmp/myrepo", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel("/tmp/myrepo/.git", path)
		if err != nil {
			return nil
		}

		if ! strings.HasPrefix(rel, "../") {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		file := yaml.NewYAMLFile(path)

		err = file.Load()
		if err != nil {
			return err
		}

		mappings = append(mappings, file.Objects...)
		return nil
	})
}

func (r *Repo) FindObjectInRepo(obj runtime.Object) (*yaml.ObjectMapping, error) {
	var found *yaml.ObjectMapping

	objectMappings, err := r.LoadReposYAMLs()
	if err != nil {
		return found, err
	}

	for _, objMapping := range objectMappings {
		if ! objMapping.Matches(obj) {
			continue
		}

		found = objMapping
		break
	}

	return found, nil
}
