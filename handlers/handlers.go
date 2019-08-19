package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/sotomskir/mastermind-server/dto"
	"github.com/sotomskir/mastermind-server/models"
	"github.com/sotomskir/mastermind-worker/amqpService"
	"github.com/sotomskir/mastermind-worker/utils"
	"golang.org/x/net/context"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
	"log"
	"os"
	"os/exec"
	"time"
)

func ProcessJobMessage(message *dto.Message) {
	job := &dto.JobMessage{}
	if err := json.Unmarshal(*message, job); err != nil {
		log.Printf("Error decoding JSON: %s", err)
	}

	log.Printf("Processing job: {ID:%d, Type:%s}", job.ID, job.Type)

	ctx, done := context.WithCancel(context.Background())
	jobLogs := make(chan dto.Message)
	go func() {
		amqpService.Publish(amqpService.Redial(ctx, os.Getenv("AMQP_URL")), jobLogs, fmt.Sprintf("job_log_%d", job.ID))
		done()
	}()
	switch job.Type {
	case models.TypeDeployment:
		processDeployment(job.ID, jobLogs)
	case models.TypeSCMPull:
		processSCMPull(job.ID, jobLogs)
	default:
		failJob(job.ID, jobLogs, fmt.Sprintf("Unsupported job type: %s", job.Type))
	}
	done()
}

func failJob(jobID uint, jobLogs chan dto.Message, message string) {
	job := models.GetJob(jobID)
	if job == nil {
		log.Printf("Job with ID: %d not found", jobID)
		return
	}
	log.Println(message)
	saveJobLog(jobLogs, job, message)
	if err := updateJobStatus(job, models.StatusFailed); err != nil {
		return
	}
}

func processSCMPull(jobID uint, jobLogs chan dto.Message) {
	job := models.GetJob(jobID)
	if job == nil {
		log.Printf("Job with ID: %d not found", jobID)
		return
	}
	if err := updateJobStatus(job, models.StatusProcessing); err != nil {
		return
	}
	if err := synchronizeProjectRepo(job, jobLogs); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot synchronize project: %s", err))
	}
	if err := updateJobStatus(job, models.StatusCompleted); err != nil {
		return
	}
}

func processDeployment(jobID uint, jobLogs chan dto.Message) {
	job := models.GetJob(jobID)
	if job == nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Deployment with ID: %d not found", jobID))
		return
	}
	if err := updateJobStatus(job, models.StatusProcessing); err != nil {
		return
	}
	if err := synchronizeProjectRepo(job, jobLogs); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot synchronize project: %s", err))
	}
	if err := utils.WriteKey(job.Inventory.Key.ID, job.Inventory.Key.Key); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot write key: %s", err))
	}

	keyPath := fmt.Sprintf("storage/keys/%d", job.Inventory.Key.ID)
	version := fmt.Sprintf("version=%s", job.Version)
	app := fmt.Sprintf("app=%s", job.Application.AnsibleName)
	cmd := exec.Command("ansible-playbook", "--private-key", keyPath, "-i", job.Inventory.SourceFile, "-e", app, "-e", version, job.Application.AnsiblePlaybook)
	cmd.Dir = fmt.Sprintf("storage/repositories/%s", job.Application.Project.Name)
	cmd.Env = []string{"ANSIBLE_FORCE_COLOR=true"}
	processPipes(cmd, jobLogs, job)

	if err := cmd.Start(); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot start command: %s", err))
	}
	if err := cmd.Wait(); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Error waiting for process: %s", err))
	}

	job.Status = models.StatusCompleted
	if cmd.ProcessState.ExitCode() != 0 {
		job.Status = models.StatusFailed
	}

	if err := updateJobStatus(job, job.Status); err != nil {
		log.Printf("Cannot update job status: %s", err)
		return
	}
}

func processPipes(cmd *exec.Cmd, jobLogs chan dto.Message, job *models.Job) {
	cmdOutReader, err := cmd.StdoutPipe()
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot get stdout pipe: %s", err))
	}

	cmdErrReader, err := cmd.StderrPipe()
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot get stderr pipe: %s", err))
	}

	outScanner := bufio.NewScanner(cmdOutReader)
	errScanner := bufio.NewScanner(cmdErrReader)

	go func() {
		for outScanner.Scan() {
			saveJobLog(jobLogs, job, outScanner.Text())
		}
	}()

	go func() {
		for errScanner.Scan() {
			saveJobLog(jobLogs, job, errScanner.Text())
		}
	}()
}

func saveJobLog(jobLogs chan dto.Message, job *models.Job, message string) {
	models.SaveJobLog(&models.JobLog{Job: *job, Message: message})
	jobLogs <- []byte(message)
}

func updateJobStatus(job *models.Job, status models.Status) error {
	err := models.UpdateJobStatus(job, map[string]interface{}{"started_at": time.Now(), "status": status})
	if err != nil {
		log.Printf("Failed to update job status: %s", err)
		return err
	}
	amqpService.JobStatuses <- dto.NewStatusMessage(job.Type, job.ID, job.Status)
	return nil
}

func synchronizeProjectRepo(job *models.Job, jobLogs chan dto.Message) error {
	var projectID uint
	if job.ProjectID != 0 {
		projectID = job.ProjectID
	}
	if job.Application.ProjectID != 0 {
		projectID = job.Application.ProjectID
	}
	project := models.GetProject(projectID)
	if project == nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Project not found: %d", projectID))
		return fmt.Errorf("not found")
	}
	path := fmt.Sprintf("./storage/repositories/%s", project.Name)
	var (
		repo *git.Repository
		err error
	)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		var keys *ssh.PublicKeys
		if project.SshKeyID != 0 {
			keys, err = ssh.NewPublicKeys(project.RepoUser, []byte(project.SshKey.Key), "")
			if err != nil {
				saveJobLog(jobLogs, job, fmt.Sprintf("NewPublicKeys: %s", err))
				return err
			}
			saveJobLog(jobLogs, job, "git clone")
			repo, err = git.PlainClone(fmt.Sprintf("./storage/repositories/%s", project.Name), false, &git.CloneOptions{
				URL:      project.RepoUrl,
				Progress: os.Stdout,
				Auth: keys,
			})
		} else {
			saveJobLog(jobLogs, job, "git clone")
			repo, err = git.PlainClone(fmt.Sprintf("./storage/repositories/%s", project.Name), false, &git.CloneOptions{
				URL:      project.RepoUrl,
				Progress: os.Stdout,
			})
		}
		if err != nil {
			saveJobLog(jobLogs, job, fmt.Sprintf("git clone: %s", err))
			return err
		}
	} else {
		saveJobLog(jobLogs, job, "git open")
		repo, err = git.PlainOpen(path)
		if err != nil {
			saveJobLog(jobLogs, job, fmt.Sprintf("git open: %s", err))
			return err
		}
		saveJobLog(jobLogs, job, "git fetch")
		err = repo.Fetch(&git.FetchOptions{RemoteName: "origin"})
		if err != nil && err.Error() != "already up-to-date" {
			saveJobLog(jobLogs, job, fmt.Sprintf("git fetch: %s", err))
			return err
		}
	}
	saveJobLog(jobLogs, job, "git tree")
	wTree, err := repo.Worktree()
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("git tree: %s", err))
		return err
	}
	saveJobLog(jobLogs, job, fmt.Sprintf("git rev-parse origin/%s", project.RepoBranch))
	hash, err := repo.ResolveRevision(plumbing.Revision(fmt.Sprintf("origin/%s", project.RepoBranch)))
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("git rev-parse: %s", err))
		return err
	}
	saveJobLog(jobLogs, job, fmt.Sprintf("git reset --hard %s", hash.String()))
	err = wTree.Reset(&git.ResetOptions{Mode:git.HardReset, Commit:*hash})
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("git reset: %s", err))
		return err
	}
	return nil
}
