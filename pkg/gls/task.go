package gls

import (
	"glsync/pkg/git"
	"glsync/pkg/gitlab"
	"go.uber.org/atomic"
	"sync"
)

type ProjectPair struct {
	GitlabProject *gitlab.Project
	LocalProject  *git.Project
}

type Action string

const (
	Clone  Action = "clone"
	Pull   Action = "pull"
	Delete Action = "delete"
)

type Status string

const (
	Open        Status = "open"
	Progressing Status = "progressing"
	Completed   Status = "completed"
)

type Task struct {
	Path        string
	LocalPath   string
	Branch      string
	ProjectPair *ProjectPair
	Action      Action
	status      atomic.String
	message     atomic.String
	error       atomic.Error
}

func NewTask(path string, projectPair *ProjectPair, localPath string, branch string, action Action, status Status) *Task {
	task := &Task{
		Path:        path,
		ProjectPair: projectPair,
		LocalPath:   localPath,
		Branch:      branch,
		Action:      action,
	}
	task.SetStatus(status)
	return task
}

func (t *Task) SetStatus(status Status) {
	t.status.Store(string(status))
}

func (t *Task) GetStatus() Status {
	return Status(t.status.Load())
}

func (t *Task) SetMessage(message string) {
	t.message.Store(message)
}

func (t *Task) GetMessage() string {
	return t.message.Load()
}

func (t *Task) SetError(err error) {
	t.error.Store(err)
}

func (t *Task) GetError() error {
	return t.error.Load()
}

func FilterTasks(tasks []*Task, status Status) []*Task {
	var result []*Task
	for _, task := range tasks {
		if task.GetStatus() == status {
			result = append(result, task)
		}
	}
	return result
}

func RunTasks(tasks []*Task, numWorkers int, work func(*Task) error) {
	taskQueue := make(chan *Task, len(tasks))
	var wg sync.WaitGroup
	for i := 1; i <= numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskQueue {
				task.SetStatus(Progressing)
				task.SetError(work(task))
				task.SetStatus(Completed)
			}
		}()
	}

	for _, task := range tasks {
		taskQueue <- task
	}

	close(taskQueue)
	wg.Wait()
}
