package git

import (
	"github.com/go-git/go-git/v5"
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
