package gitlab

import (
	"fmt"
	"gitlab.com/gitlab-org/api/client-go"
	"strings"
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

func listProjectsRecursively(gl *gitlab.Client, group *gitlab.Group, progress func(string)) ([]*gitlab.Project, error) {
	progress(group.FullPath)

	projects, _, err := gl.Groups.ListGroupProjects(group.ID, nil)
	if err != nil {
		return nil, err
	}

	subgroups, _, err := gl.Groups.ListSubGroups(group.ID, nil)
	if err != nil {
		return nil, err
	}

	for _, subgroup := range subgroups {
		subprojects, err := listProjectsRecursively(gl, subgroup, progress)
		if err != nil {
			return nil, err
		}

		projects = append(projects, subprojects...)
	}

	return projects, nil
}
