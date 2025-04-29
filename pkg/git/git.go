package git

import (
	"errors"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"os"
	"path/filepath"
	"strings"
)

type Git struct {
	auth *ssh.PublicKeys
}

type Project struct {
	Path   string
	Branch string
}

func New(keyFile string, pass string) (*Git, error) {
	auth, err := ssh.NewPublicKeysFromFile("git", keyFile, pass)

	if err != nil {
		return nil, err
	}

	gi := Git{
		auth: auth,
	}

	return &gi, nil
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

func DeleteProject(localPath string) error {
	_, err := git.PlainOpen(localPath)
	if err != nil {
		return err // folder not a git repo
	}

	return os.RemoveAll(localPath)
}

func (gi *Git) CloneProject(cloneUrl string, localPath string) error {
	_, err := git.PlainClone(localPath, false, &git.CloneOptions{
		URL:  cloneUrl,
		Auth: gi.auth,
	})
	return err
}

func (gi *Git) PullProject(localPath string) error {
	repo, err := git.PlainOpen(localPath)
	if err != nil {
		return err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}

	err = worktree.Pull(&git.PullOptions{
		RemoteName: "origin",
		Auth:       gi.auth,
	})

	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}

	return err
}
