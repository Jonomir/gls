package git

import (
	"github.com/go-git/go-git/v5"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Project struct {
	Path   string
	Branch string
}

func GetLocalProjects(localPath string) ([]*Project, error) {
	var projects []*Project

	err := filepath.WalkDir(localPath, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !e.IsDir() {
			return nil // it's a file
		}

		repo, err := git.PlainOpen(path)
		if err != nil {
			return nil // folder not a git repo
		}

		headRef, err := repo.Head()
		if err != nil {
			return err
		}

		projects = append(projects, &Project{
			Path:   strings.TrimPrefix(path, localPath+"/"),
			Branch: headRef.Name().Short(),
		})

		return filepath.SkipDir // found a repo, don't need to check subtree
	})

	return projects, err
}

func CloneProject(cloneUrl string, localPath string, progress io.Writer) error {
	_, err := git.PlainClone(localPath, false, &git.CloneOptions{
		URL:      cloneUrl,
		Progress: progress,
	})

	return err
}

func PullProject(localPath string, progress io.Writer) error {
	repo, err := git.PlainOpen(localPath)
	if err != nil {
		return err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}

	err = worktree.Pull(&git.PullOptions{
		Progress: progress,
	})

	if err == git.NoErrAlreadyUpToDate {
		return nil
	}

	if err != nil {
		return err
	}

	return nil
}

func DeleteProject(localPath string) error {
	return os.RemoveAll(localPath)
}
