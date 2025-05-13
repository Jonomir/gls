package git

import (
	"bufio"
	"fmt"
	"github.com/go-git/go-git/v5"
	"os"
	"os/exec"
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

func DeleteProject(localPath string) error {
	_, err := git.PlainOpen(localPath)
	if err != nil {
		return err // folder not a git repo
	}

	return os.RemoveAll(localPath)
}

// It would be nice to use go-git for clone and pull too, but go-git pull overwrites existing changes in the repo
// It also requires configuring an SSH key. While just running git in the right place already does all this for you

func CloneProject(cloneUrl string, localPath string, lineProcessor func(string)) error {
	cmd := exec.Command("git", "clone", "--progress", cloneUrl, localPath)
	return execCommand(cmd, lineProcessor)
}

func PullProject(localPath string, lineProcessor func(string)) error {
	cmd := exec.Command("git", "pull", "--progress")
	cmd.Dir = localPath
	return execCommand(cmd, lineProcessor)
}

func execCommand(cmd *exec.Cmd, lineProcessor func(string)) error {
	stderr, err := cmd.StderrPipe() // git reports progress on stderr
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	var out string
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanLines)
	for scanner.Scan() {
		line := scanner.Text()
		out += fmt.Sprintln(line)
		lineProcessor(line)
	}

	err = scanner.Err()
	if err != nil {
		return err
	}

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, out)
	}

	return nil
}

func scanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}
