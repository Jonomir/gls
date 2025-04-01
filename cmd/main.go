package main

import (
	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigdotenv"
	"github.com/jedib0t/go-pretty/v6/progress"
	"github.com/jedib0t/go-pretty/v6/text"
	"glsync/pkg/git"
	"glsync/pkg/gitlab"
	"go.uber.org/atomic"
	"log"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
)

type Config struct {
	Gitlab struct {
		Url   string `default:"https://gitlab.com" usage:"Gitlab URL"`
		Token string `required:"true" usage:"Gitlab token for authentication"`
	}
	Path struct {
		Gitlab string `required:"true" usage:"Gitlab group to clone recursively"`
		Local  string `required:"true" usage:"Local path to clone to"`
	}
}

func loadConfig() Config {
	homedir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error getting homedir: %v", err)
	}

	var cfg Config
	loader := aconfig.LoaderFor(&cfg, aconfig.Config{
		EnvPrefix:     "GLS",
		FlagDelimiter: "-",

		Files: []string{homedir + "/.gls"},
		FileDecoders: map[string]aconfig.FileDecoder{
			".gls": aconfigdotenv.New(),
		},
	})

	flags := loader.Flags()
	helpFlag := flags.Bool("help", false, "Display help message")

	err = flags.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Error parsing flags: %v", err)
	}

	if *helpFlag {
		println("Usage: gls [flags]")
		flags.PrintDefaults()
		println("Flags can also be passed via environment variables with prefix 'GLS_'")
		println("Or via file at $HOME/.gls in format KEY=value")
		os.Exit(0)
	}

	if err := loader.Load(); err != nil {
		log.Fatalf("Error loading config: %v", err)
	}
	return cfg
}

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

type Task struct {
	Path        string
	ProjectPair *ProjectPair
	Action      Action
	Tracker     *progress.Tracker
	Skipped     bool
	Error       atomic.Error
}

func main() {
	cfg := loadConfig()

	gl, err := gitlab.New(cfg.Gitlab.Url, cfg.Gitlab.Token)
	if err != nil {
		log.Fatalf("Error creating gitlab client: %v", err)
	}

	println(text.FgCyan.Sprintf("Fetching active Gitlab projects from %s", cfg.Gitlab.Url))
	gitlabProjects, err := gl.GetActiveGitlabProjects(cfg.Path.Gitlab)
	if err != nil {
		log.Fatalf("Error getting gitlab projects: %v", err)
	}

	println(text.FgCyan.Sprintf("Loading local projects in %s", cfg.Path.Local))
	localProjects, err := git.GetLocalProjects(cfg.Path.Local)
	if err != nil {
		log.Fatalf("Error getting local projects: %v", err)
	}

	println(text.FgCyan.Sprintf("Creating tasklist"))
	tasks := createTasks(gitlabProjects, localProjects, cfg.Path.Local)

	pw := progress.NewWriter()
	pw.SetMessageLength(50)
	pw.SetNumTrackersExpected(len(tasks))
	pw.SetSortBy(progress.SortByMessage)
	pw.SetStyle(progress.StyleDefault)
	pw.SetTrackerLength(40)
	pw.SetTrackerPosition(progress.PositionRight)
	pw.SetUpdateFrequency(time.Millisecond * 100)
	pw.Style().Visibility.Value = false
	pw.Style().Colors = progress.StyleColorsExample
	pw.Style().Colors.Percent = text.Colors{text.FgCyan}

	go pw.Render()

	println(text.FgCyan.Sprintf("Starting task execution"))
	executeTasks(tasks, 5, pw)

	time.Sleep(time.Millisecond * 100) // wait for one more render cycle
	pw.Stop()

	for _, task := range tasks {
		if task.Error.Load() != nil {
			println(text.FgRed.Sprintf("Failed to %s %s %v", task.Action, task.Path, task.Error.Load()))
		}
	}
}

func executeTasks(tasks []*Task, numWorkers int, pw progress.Writer) {
	taskQueue := make(chan *Task, len(tasks))
	var wg sync.WaitGroup
	for i := 1; i <= numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskQueue {
				pw.AppendTracker(task.Tracker)
				if task.Skipped {
					task.Tracker.MarkAsDone()
				} else {
					task.Tracker.Start()
					err := executeTask(task)
					if err != nil {
						task.Tracker.MarkAsErrored()
						task.Error.Store(err)
					} else {
						task.Tracker.MarkAsDone()
					}
				}
			}
		}()
	}

	for _, task := range tasks {
		taskQueue <- task
	}

	close(taskQueue)
	wg.Wait()
}

func executeTask(task *Task) error {
	pattern := regexp.MustCompile(`^Receiving objects:.*\((\d+)/(\d+)\)`)

	lineProcessor := func(line string) {
		matches := pattern.FindStringSubmatch(line)

		if len(matches) == 3 { // matches[0] is the full match, [1] and [2] are the two numbers
			current, _ := strconv.Atoi(matches[1])
			total, _ := strconv.Atoi(matches[2])
			task.Tracker.UpdateTotal(int64(total))
			task.Tracker.SetValue(int64(current))
		}
	}

	switch task.Action {
	case Clone:
		return git.CloneProject(task.ProjectPair.GitlabProject.CloneUrl, task.Path, lineProcessor)
	case Pull:
		return git.PullProject(task.Path, lineProcessor)
	case Delete:
		return git.DeleteProject(task.Path)
	}
	return nil
}

func createTasks(gitlabProjects []*gitlab.Project, localProjects []*git.Project, localPath string) []*Task {
	var tasks []*Task
	for key, projectPair := range pairProjects(gitlabProjects, localProjects) {
		var task *Task

		// We have a remote and local copy, only need to pull
		if projectPair.GitlabProject != nil && projectPair.LocalProject != nil {
			if projectPair.GitlabProject.DefaultBranch == projectPair.LocalProject.Branch {
				task = &Task{
					Path:        localPath + "/" + key,
					ProjectPair: projectPair,
					Action:      Pull,
					Tracker: &progress.Tracker{
						Message: "Pulling " + key + " " + projectPair.LocalProject.Branch,
					},
				}
			} else {
				task = &Task{
					Path:        localPath + "/" + key,
					ProjectPair: projectPair,
					Action:      Pull,
					Skipped:     true,
					Tracker: &progress.Tracker{
						Message: "Skipped pulling " + key + " " + projectPair.LocalProject.Branch,
					},
				}
			}
		}

		// We don't have a local copy, so we clone
		if projectPair.GitlabProject != nil && projectPair.LocalProject == nil {
			task = &Task{
				Path:        localPath + "/" + key,
				ProjectPair: projectPair,
				Action:      Clone,
				Tracker: &progress.Tracker{
					Message: "Cloning " + key + " " + projectPair.GitlabProject.DefaultBranch,
				},
			}
		}

		// We only have a local copy, ask if we should delete it
		if projectPair.GitlabProject == nil && projectPair.LocalProject != nil {

			//TODO implement user prompt
			if true {
				task = &Task{
					Path:        localPath + "/" + key,
					ProjectPair: projectPair,
					Action:      Delete,
					Tracker: &progress.Tracker{
						Message: "Deleting " + key + " " + projectPair.LocalProject.Branch,
					},
				}
			} else {
				task = &Task{
					Path:        localPath + "/" + key,
					ProjectPair: projectPair,
					Action:      Delete,
					Skipped:     true,
					Tracker: &progress.Tracker{
						Message: "Skipped deleting " + key + " " + projectPair.LocalProject.Branch,
					},
				}
			}
		}

		tasks = append(tasks, task)
	}

	return tasks
}

func pairProjects(gitlabProjects []*gitlab.Project, localProjects []*git.Project) map[string]*ProjectPair {
	projectPairs := make(map[string]*ProjectPair)
	for _, project := range gitlabProjects {
		projectPair := projectPairs[project.Path]
		if projectPair == nil {
			projectPair = &ProjectPair{}
		}

		projectPair.GitlabProject = project
		projectPairs[project.Path] = projectPair
	}

	for _, project := range localProjects {
		projectPair := projectPairs[project.Path]
		if projectPair == nil {
			projectPair = &ProjectPair{}
		}

		projectPair.LocalProject = project
		projectPairs[project.Path] = projectPair
	}
	return projectPairs
}
