package gitlab

import (
	"fmt"
	"strings"
	"sync"

	"gitlab.com/gitlab-org/api/client-go/v3"
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
	var requestOptions []gitlab.RequestOptionFunc
	for {
		groups, response, err := gl.Groups.SearchGroup(path, requestOptions...)
		if err != nil {
			return nil, err
		}

		for _, group := range groups {
			if group.FullPath == path {
				return group, nil
			}
		}

		next, hasNext := gitlab.WithNext(response)
		if !hasNext {
			break
		}
		requestOptions = []gitlab.RequestOptionFunc{next}
	}

	return nil, nil
}

func listProjectsRecursively(gl *gitlab.Client, group *gitlab.Group, progress func(string), resChan chan *gitlab.Project, errChan chan error, wg *sync.WaitGroup) {
	progress(group.FullPath)
	wg.Add(2)

	go func() {
		defer wg.Done()

		var requestOptions []gitlab.RequestOptionFunc
		for {
			projects, response, err := gl.Groups.ListGroupProjects(group.ID, nil, requestOptions...)
			if err != nil {
				errChan <- err
				return
			}

			for _, project := range projects {
				resChan <- project
			}

			next, hasNext := gitlab.WithNext(response)
			if !hasNext {
				break
			}
			requestOptions = []gitlab.RequestOptionFunc{next}
		}
	}()

	go func() {
		defer wg.Done()

		var requestOptions []gitlab.RequestOptionFunc
		for {
			subgroups, response, err := gl.Groups.ListSubGroups(group.ID, nil, requestOptions...)
			if err != nil {
				errChan <- err
				return
			}

			for _, subgroup := range subgroups {
				listProjectsRecursively(gl, subgroup, progress, resChan, errChan, wg)
			}

			next, hasNext := gitlab.WithNext(response)
			if !hasNext {
				break
			}
			requestOptions = []gitlab.RequestOptionFunc{next}
		}
	}()
}
