package main

import (
	"bufio"
	"fmt"
	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigdotenv"
	"github.com/jedib0t/go-pretty/v6/progress"
	"github.com/jedib0t/go-pretty/v6/text"
	"gls/pkg/git"
	"gls/pkg/gitlab"
	"go.uber.org/atomic"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Workers int `default:"5" usage:"Number of parallel workers"`
	Gitlab  struct {
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

type Action string

const (
	Clone  Action = "clone"
	Pull   Action = "pull"
	Delete Action = "delete"
)

type Task struct {
	Path     string
	CloneUrl string
	Action   Action
	Tracker  *progress.Tracker
	Skipped  bool
	Error    atomic.Error
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

	println(text.FgCyan.Sprintf("Determining actions"))

	tasks, header := createTasks(gitlabProjects, localProjects, cfg.Path.Local)

	var messageLength = 0
	for _, task := range tasks {
		if len(task.Tracker.Message) > messageLength {
			messageLength = len(task.Tracker.Message)
		}
	}

	pw := progress.NewWriter()
	pw.SetUpdateFrequency(time.Millisecond * 100)
	pw.SetNumTrackersExpected(len(tasks))
	pw.SetSortBy(progress.SortByMessage)
	pw.SetTrackerPosition(progress.PositionRight)
	pw.SetMessageLength(messageLength)
	pw.SetTrackerLength(40)

	pw.SetStyle(progress.StyleDefault)
	pw.Style().Visibility.Value = false
	pw.Style().Options.Separator = ""
	pw.Style().Options.DoneString = "done"
	pw.Style().Options.ErrorString = "error"

	pw.Style().Colors = progress.StyleColorsExample
	pw.Style().Colors.Percent = text.Colors{text.FgCyan}
	pw.Style().Colors.Error = text.Colors{text.FgHiRed}

	pw.Style().Options.TimeInProgressPrecision = time.Millisecond
	pw.Style().Options.TimeDonePrecision = time.Millisecond

	println(text.FgHiGreen.Sprintf("\n%s", header))
	go pw.Render()

	executeTasks(tasks, cfg.Workers, pw)

	time.Sleep(time.Millisecond * 100) // wait for one more render cycle
	pw.Stop()

	for _, task := range tasks {
		if task.Error.Load() != nil {
			println(text.FgHiRed.Sprintf("Failed to %s %s %v", task.Action, task.Path, task.Error.Load()))
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
		return git.CloneProject(task.CloneUrl, task.Path, lineProcessor)
	case Pull:
		return git.PullProject(task.Path, lineProcessor)
	case Delete:
		return git.DeleteProject(task.Path)
	}
	return nil
}

type InternalTask struct {
	Key      string
	Action   Action
	CloneUrl string
	Skipped  bool
	Message  string
	Branch   string
}

func createTasks(gitlabProjects []*gitlab.Project, localProjects []*git.Project, localPath string) ([]*Task, string) {
	var internalTasks []*InternalTask
	for key, projectPair := range pairProjects(gitlabProjects, localProjects) {
		// We have a remote and local copy, only need to pull
		if projectPair.GitlabProject != nil && projectPair.LocalProject != nil {
			if projectPair.GitlabProject.DefaultBranch == projectPair.LocalProject.Branch {
				internalTasks = append(internalTasks, &InternalTask{
					Key:     key,
					Action:  Pull,
					Message: "Pulling",
					Branch:  projectPair.LocalProject.Branch,
				})
			} else {
				internalTasks = append(internalTasks, &InternalTask{
					Key:     key,
					Action:  Pull,
					Skipped: true,
					Message: "Skipped pulling",
					Branch:  projectPair.LocalProject.Branch,
				})
			}
		}

		// We don't have a local copy, so we clone
		if projectPair.GitlabProject != nil && projectPair.LocalProject == nil {
			internalTasks = append(internalTasks, &InternalTask{
				Key:      key,
				Action:   Clone,
				Message:  "Cloning",
				CloneUrl: projectPair.GitlabProject.CloneUrl,
				Branch:   projectPair.GitlabProject.DefaultBranch,
			})
		}

		// We only have a local copy, ask if we should delete it
		if projectPair.GitlabProject == nil && projectPair.LocalProject != nil {

			if askForConfirmation(text.FgMagenta.Sprintf("Do you want to delete %s?", key)) {
				internalTasks = append(internalTasks, &InternalTask{
					Key:     key,
					Action:  Delete,
					Message: "Deleting",
					Branch:  projectPair.LocalProject.Branch,
				})
			} else {
				internalTasks = append(internalTasks, &InternalTask{
					Key:     key,
					Action:  Delete,
					Skipped: true,
					Message: "Skipped deletion",
					Branch:  projectPair.LocalProject.Branch,
				})
			}
		}
	}

	var messageHeader = "Action"
	var keyHeader = "Project"
	var branchHeader = "Branch"
	var statusHeader = "Status"

	var messageLength = len(messageHeader)
	var keyLength = len(keyHeader)
	var branchLength = len(branchHeader)
	for _, internalTask := range internalTasks {
		if len(internalTask.Message) > messageLength {
			messageLength = len(internalTask.Message)
		}
		if len(internalTask.Key) > keyLength {
			keyLength = len(internalTask.Key)
		}
		if len(internalTask.Branch) > branchLength {
			branchLength = len(internalTask.Branch)
		}
	}

	var tasks []*Task
	for _, internalTask := range internalTasks {
		tasks = append(tasks, &Task{
			Path:     localPath + "/" + internalTask.Key,
			CloneUrl: internalTask.CloneUrl,
			Action:   internalTask.Action,
			Skipped:  internalTask.Skipped,
			Error:    atomic.Error{},
			Tracker: &progress.Tracker{
				Message: text.Pad(internalTask.Message, messageLength+2, ' ') +
					text.Pad(internalTask.Key, keyLength+2, ' ') +
					text.Pad(internalTask.Branch, branchLength+2, ' '),
			},
		})
	}

	header := text.Pad(messageHeader, messageLength+2, ' ') +
		text.Pad(keyHeader, keyLength+2, ' ') +
		text.Pad(branchHeader, branchLength+2, ' ') +
		statusHeader

	return tasks, header
}

type ProjectPair struct {
	GitlabProject *gitlab.Project
	LocalProject  *git.Project
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

func askForConfirmation(promt string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", promt)

		response, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("Error reading input: %v", err)
		}

		response = strings.ToLower(strings.TrimSpace(response))

		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
	}
}
