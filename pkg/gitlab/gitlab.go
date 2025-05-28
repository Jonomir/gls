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

func (gl *Gitlab) GetActiveGitlabProjects(groupPath string, progress func(string)) ([]*Project, error) {

	group, err := getGroupByPath(gl.client, groupPath)
	if err != nil {
		return nil, err
	}

	if group == nil {
		return nil, fmt.Errorf("group %s not found", groupPath)
	}

	gitlabProjects, err := listProjectsRecursively(gl.client, group, progress)
	if err != nil {
		return nil, err
	}

	var projects []*Project
	for _, project := range gitlabProjects {
		// We only care about projects that are not archived and not shared with us
		if !project.Archived && len(project.SharedWithGroups) == 0 {
			projects = append(projects, &Project{
				Path:          strings.TrimPrefix(project.PathWithNamespace, groupPath+"/"),
				DefaultBranch: project.DefaultBranch,
				CloneUrl:      project.SSHURLToRepo,
			})
		}
	}

	return projects, nil
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

type Result struct {
	Projects []*gitlab.Project
	Err      error
}

func listProjectsRecursively(gl *gitlab.Client, group *gitlab.Group, progress func(string)) ([]*gitlab.Project, error) {
	progress(group.FullPath)

	var wg sync.WaitGroup
	var projects []*gitlab.Project
	var subgroups []*gitlab.Group
	var errProjects error
	var errSubgroups error

	wg.Add(2)

	go func() {
		defer wg.Done()
		projects, _, errProjects = gl.Groups.ListGroupProjects(group.ID, nil)
	}()

	go func() {
		defer wg.Done()
		subgroups, _, errSubgroups = gl.Groups.ListSubGroups(group.ID, nil)
	}()

	wg.Wait()

	if errProjects != nil {
		return nil, errProjects
	}

	if errSubgroups != nil {
		return nil, errSubgroups
	}

	if len(subgroups) > 0 {
		var wg sync.WaitGroup

		resultsChan := make(chan Result, len(subgroups))

		for _, subgroup := range subgroups {
			wg.Add(1)

			go func() {
				defer wg.Done()
				subprojects, err := listProjectsRecursively(gl, subgroup, progress)
				resultsChan <- Result{
					Projects: subprojects,
					Err:      err,
				}
			}()
		}

		wg.Wait()
		close(resultsChan)

		for res := range resultsChan {
			if res.Err != nil {
				return nil, res.Err
			}

			projects = append(projects, res.Projects...)
		}
	}

	return projects, nil
}
