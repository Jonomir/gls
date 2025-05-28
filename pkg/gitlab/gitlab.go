package gitlab

import (
	"fmt"
	"gitlab.com/gitlab-org/api/client-go"
	"strings"
	"sync"
)

type Gitlab struct {
	client *gitlab.Client
}

type Project struct {
	Path          string
	DefaultBranch string
	CloneUrl      string
}

func New(url string, token string) (*Gitlab, error) {
	client, err := gitlab.NewClient(token, gitlab.WithBaseURL(url))
	if err != nil {
		return nil, err
	}

	gl := Gitlab{
		client: client,
	}

	return &gl, nil
}

func (gl *Gitlab) GetActiveGitlabProjects(groupPath string, progress func(string)) ([]*Project, []error) {

	group, err := getGroupByPath(gl.client, groupPath)
	if err != nil {
		return nil, []error{err}
	}

	if group == nil {
		return nil, []error{fmt.Errorf("group %s not found", groupPath)}
	}

	var resChan = make(chan *gitlab.Project)
	var errChan = make(chan error)

	var pwg sync.WaitGroup
	listProjectsRecursively(gl.client, group, progress, resChan, errChan, &pwg)

	var result []*Project
	var errors []error

	var cwg sync.WaitGroup
	cwg.Add(3)

	go func() {
		defer cwg.Done()

		pwg.Wait()
		close(resChan)
		close(errChan)
	}()

	go func() {
		defer cwg.Done()

		for project := range resChan {
			if !project.Archived && len(project.SharedWithGroups) == 0 {
				result = append(result, &Project{
					Path:          strings.TrimPrefix(project.PathWithNamespace, groupPath+"/"),
					DefaultBranch: project.DefaultBranch,
					CloneUrl:      project.SSHURLToRepo,
				})
			}
		}
	}()

	go func() {
		defer cwg.Done()

		for err := range errChan {
			errors = append(errors, err)
		}
	}()

	cwg.Wait()
	return result, errors
}

func getGroupByPath(gl *gitlab.Client, path string) (*gitlab.Group, error) {
	groups, _, err := gl.Groups.SearchGroup(path)
	if err != nil {
		return nil, err
	}

	for _, group := range groups {
		if group.FullPath == path {
			return group, nil
		}
	}

	return nil, nil
}

func listProjectsRecursively(gl *gitlab.Client, group *gitlab.Group, progress func(string), resChan chan *gitlab.Project, errChan chan error, wg *sync.WaitGroup) {
	progress(group.FullPath)
	wg.Add(2)

	go func() {
		defer wg.Done()
		projects, _, err := gl.Groups.ListGroupProjects(group.ID, nil)
		if err != nil {
			errChan <- err
		}

		for _, project := range projects {
			resChan <- project
		}
	}()

	go func() {
		defer wg.Done()
		subgroups, _, err := gl.Groups.ListSubGroups(group.ID, nil)
		if err != nil {
			errChan <- err
		}

		for _, subgroup := range subgroups {
			listProjectsRecursively(gl, subgroup, progress, resChan, errChan, wg)
		}
	}()
}
