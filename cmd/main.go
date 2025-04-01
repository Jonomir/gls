package main

import (
	"atomicgo.dev/cursor"
	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigdotenv"
	"github.com/fatih/color"
	"github.com/rodaine/table"
	"glsync/pkg/git"
	"glsync/pkg/gitlab"
	"go.uber.org/atomic"
	"log"
	"math/rand"
	"os"
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
	Branch      string
	Action      Action
	ProjectPair *ProjectPair
	Status      atomic.String
	Error       atomic.Error
	Completed   atomic.Bool
}

func main() {
	cfg := loadConfig()

	gl, err := gitlab.New(cfg.Gitlab.Url, cfg.Gitlab.Token)
	if err != nil {
		log.Fatalf("Error creating gitlab client: %v", err)
	}

	gitlabProjects, err := gl.GetActiveGitlabProjects(cfg.Path.Gitlab)
	if err != nil {
		log.Fatalf("Error getting gitlab projects: %v", err)
	}

	localProjects, err := git.GetLocalProjects(cfg.Path.Local)
	if err != nil {
		log.Fatalf("Error getting local projects: %v", err)
	}

	tasks := createTasks(gitlabProjects, localProjects)
	openTasks := getOpenTasks(tasks)

	go renderer(tasks)

	runTasks(openTasks, 5)
}

func getOpenTasks(tasks []*Task) []*Task {
	var openTasks []*Task
	for _, task := range tasks {
		if !task.Completed.Load() {
			openTasks = append(openTasks, task)
		}
	}
	return openTasks
}

func runTasks(tasks []*Task, numWorkers int) {
	taskQueue := make(chan *Task, len(tasks))
	var wg sync.WaitGroup
	for i := 1; i <= numWorkers; i++ {
		wg.Add(1)
		go worker(taskQueue, &wg)
	}

	for _, task := range tasks {
		taskQueue <- task
	}

	close(taskQueue)
	wg.Wait()
}

func renderer(tasks []*Task) {
	printTable(tasks)

	for {
		cursor.ClearLinesUp(len(tasks) + 1)
		printTable(tasks)

		time.Sleep(200 * time.Millisecond)
	}
}

func printTable(tasks []*Task) {
	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	tbl := table.New("Repo", "Branch", "Action", "Status")
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for _, task := range tasks {
		tbl.AddRow(task.Path, task.Branch, task.Action, task.Status.Load())
	}

	tbl.Print()
}

func worker(taskQueue <-chan *Task, wg *sync.WaitGroup) {
	defer wg.Done()

	for task := range taskQueue {

		for i := 0; i < 300; i++ {

			task.Status.Store("Progressing " + strconv.Itoa(i))
			time.Sleep(time.Millisecond * time.Duration(rand.Intn(500)))
		}
	}
}

func createTasks(gitlabProjects []*gitlab.Project, localProjects []*git.Project) []*Task {
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

	var tasks []*Task
	for key, projectPair := range projectPairs {

		// We have a remote and local copy, only need to pull
		if projectPair.GitlabProject != nil && projectPair.LocalProject != nil {

			if projectPair.GitlabProject.DefaultBranch == projectPair.LocalProject.Branch {
				// Default branch is checked out, we can pull
				tasks = append(tasks, &Task{
					Path:        key,
					Branch:      projectPair.LocalProject.Branch,
					ProjectPair: projectPair,
					Action:      Pull,
					Completed:   *atomic.NewBool(false),
					Status:      *atomic.NewString("Waiting..."),
				})
			} else {
				// Non default branch checked out, skip pulling
				tasks = append(tasks, &Task{
					Path:        key,
					Branch:      projectPair.LocalProject.Branch,
					ProjectPair: projectPair,
					Action:      Pull,
					Completed:   *atomic.NewBool(true),
					Status:      *atomic.NewString("Skipped"),
				})
			}

		}

		// We don't have a local copy, so we clone
		if projectPair.GitlabProject != nil && projectPair.LocalProject == nil {
			tasks = append(tasks, &Task{
				Path:        key,
				Branch:      projectPair.GitlabProject.DefaultBranch,
				ProjectPair: projectPair,
				Action:      Clone,
				Completed:   *atomic.NewBool(false),
				Status:      *atomic.NewString("Waiting..."),
			})
		}

		// We only have a local copy, ask if we should delete it
		if projectPair.GitlabProject == nil && projectPair.LocalProject != nil {

			//TODO implement user prompt
			if true {
				// We should delete
				tasks = append(tasks, &Task{
					Path:        key,
					Branch:      projectPair.LocalProject.Branch,
					ProjectPair: projectPair,
					Action:      Delete,
					Completed:   *atomic.NewBool(false),
					Status:      *atomic.NewString("Waiting..."),
				})
			} else {
				// user skipped
				tasks = append(tasks, &Task{
					Path:        key,
					Branch:      projectPair.LocalProject.Branch,
					ProjectPair: projectPair,
					Action:      Delete,
					Completed:   *atomic.NewBool(true),
					Status:      *atomic.NewString("Skipped"),
				})
			}
		}

	}
	return tasks
}
