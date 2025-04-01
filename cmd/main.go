package main

import (
	"atomicgo.dev/cursor"
	"fmt"
	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigdotenv"
	"github.com/fatih/color"
	"github.com/rodaine/table"
	"glsync/pkg/git"
	"glsync/pkg/gitlab"
	"glsync/pkg/gls"
	"log"
	"os"
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
		fmt.Println("Usage: gls [flags]")
		flags.PrintDefaults()
		fmt.Println("Flags can also be passed via environment variables with prefix 'GLS_'")
		fmt.Println("Or via file at $HOME/.gls in format KEY=value")
		os.Exit(0)
	}

	if err := loader.Load(); err != nil {
		log.Fatalf("Error loading config: %v", err)
	}
	return cfg
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

	tasks := createTasks(gitlabProjects, localProjects, cfg.Path.Local)
	openTasks := gls.FilterTasks(tasks, gls.Open)

	go renderer(tasks)

	gls.RunTasks(openTasks, 5, func(task *gls.Task) error {
		switch task.Action {
		case gls.Clone:
			return git.CloneProject(task.ProjectPair.GitlabProject.CloneUrl, task.LocalPath, gls.NewLineWriter(func(line string) {
				task.SetMessage(line)
			}))
		case gls.Pull:
			return git.PullProject(task.LocalPath, gls.NewLineWriter(func(line string) {
				task.SetMessage(line)
			}))
		case gls.Delete:
			return git.DeleteProject(task.LocalPath)
		}
		return nil
	})

	for _, task := range openTasks {
		if task.GetError() != nil {
			fmt.Printf("Failed to %s %s %v\n", task.Action, task.Path, task.GetError())
		}
	}
}

func renderer(tasks []*gls.Task) {
	linesToClear := printProgressingTasksTable(tasks)

	for {
		cursor.ClearLinesUp(linesToClear)
		linesToClear = printProgressingTasksTable(tasks)

		time.Sleep(200 * time.Millisecond)
	}
}

func printProgressingTasksTable(tasks []*gls.Task) int {
	progressingTasks := gls.FilterTasks(tasks, gls.Progressing)

	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	tbl := table.New("Repo", "Branch", "Action", "Status", "Message")
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for _, task := range progressingTasks {
		tbl.AddRow(task.Path, task.Branch, task.Action, task.GetStatus(), task.GetMessage())
	}

	tbl.Print()

	return len(progressingTasks) + 1
}

func createTasks(gitlabProjects []*gitlab.Project, localProjects []*git.Project, localPath string) []*gls.Task {
	projectPairs := pairProjects(gitlabProjects, localProjects)

	var tasks []*gls.Task
	for key, projectPair := range projectPairs {
		var task *gls.Task

		// We have a remote and local copy, only need to pull
		if projectPair.GitlabProject != nil && projectPair.LocalProject != nil {
			if projectPair.GitlabProject.DefaultBranch == projectPair.LocalProject.Branch {
				task = gls.NewTask(key, projectPair,
					localPath+"/"+projectPair.LocalProject.Path,
					projectPair.LocalProject.Branch,
					gls.Pull, gls.Open)
			} else {
				task = gls.NewTask(key, projectPair,
					localPath+"/"+projectPair.LocalProject.Path,
					projectPair.LocalProject.Branch,
					gls.Pull, gls.Completed)
				task.SetMessage("Skipped, non default branch")
			}
		}

		// We don't have a local copy, so we clone
		if projectPair.GitlabProject != nil && projectPair.LocalProject == nil {
			task = gls.NewTask(key, projectPair,
				localPath+"/"+projectPair.GitlabProject.Path,
				projectPair.GitlabProject.DefaultBranch,
				gls.Clone, gls.Open)
		}

		// We only have a local copy, ask if we should delete it
		if projectPair.GitlabProject == nil && projectPair.LocalProject != nil {

			//TODO implement user prompt
			if true {
				task = gls.NewTask(key, projectPair,
					localPath+"/"+projectPair.LocalProject.Path,
					projectPair.LocalProject.Branch,
					gls.Delete, gls.Open)
			} else {
				task = gls.NewTask(key, projectPair,
					localPath+"/"+projectPair.LocalProject.Path,
					projectPair.LocalProject.Branch,
					gls.Delete, gls.Completed)
				task.SetMessage("Skipped, requested by user")
			}
		}

		tasks = append(tasks, task)
	}

	return tasks
}

func pairProjects(gitlabProjects []*gitlab.Project, localProjects []*git.Project) map[string]*gls.ProjectPair {
	projectPairs := make(map[string]*gls.ProjectPair)
	for _, project := range gitlabProjects {
		projectPair := projectPairs[project.Path]
		if projectPair == nil {
			projectPair = &gls.ProjectPair{}
		}

		projectPair.GitlabProject = project
		projectPairs[project.Path] = projectPair
	}

	for _, project := range localProjects {
		projectPair := projectPairs[project.Path]
		if projectPair == nil {
			projectPair = &gls.ProjectPair{}
		}

		projectPair.LocalProject = project
		projectPairs[project.Path] = projectPair
	}
	return projectPairs
}
