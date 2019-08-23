package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/sotomskir/mastermind-server/dto"
	"github.com/sotomskir/mastermind-server/models"
	"github.com/sotomskir/mastermind-worker/amqpService"
	"github.com/sotomskir/mastermind-worker/utils"
	ssh2 "golang.org/x/crypto/ssh"
	"golang.org/x/net/context"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
	"io/ioutil"
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
	case models.TypeJob:
		processJob(job.ID, jobLogs)
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

	keyPath := fmt.Sprintf("../../keys/%d", job.Inventory.Key.ID)
	version := fmt.Sprintf("version=%s", job.Version)
	app := fmt.Sprintf("app=%s", job.Application.AnsibleName)
	cmd := exec.Command("ansible-playbook", "--private-key", keyPath, "-i", job.Inventory.SourceFile, "-e", app, "-e", version, job.Application.AnsiblePlaybook)
	cmd.Dir = fmt.Sprintf("storage/repositories/%s", job.Application.Project.Name)
	cmd.Env = []string{"ANSIBLE_FORCE_COLOR=true"}
	processPipes(cmd, jobLogs, job)

	if err := cmd.Start(); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot start command: %s", err))
	}

	job.Status = models.StatusCompleted
	if err := cmd.Wait(); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Error waiting for process: %s", err))
		job.Status = models.StatusFailed
	}

	if err := updateJobStatus(job, job.Status); err != nil {
		log.Printf("Cannot update job status: %s", err)
		return
	}
}

func processJob(jobID uint, jobLogs chan dto.Message) {
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

	keyPath := fmt.Sprintf("../../keys/%d", job.Inventory.Key.ID)
	extraVarsFile, err := ioutil.TempFile("/tmp/", "extraVars")
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot create temp file: %s", err))
	}
	//defer os.Remove(extraVarsFile.Name())
	_, err = extraVarsFile.WriteString(job.ExtraVariables)
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot create temp file: %s", err))
	}
	fmt.Printf("job.ExtraVariables %s", job.ExtraVariables)
	fmt.Printf("job.Playboook %s", job.Playbook)
	fmt.Printf("job.extraVarsFile.Name() %s", extraVarsFile.Name())
	fmt.Printf("job.job.Inventory.SourceFile %s", job.Inventory.SourceFile)
	fmt.Printf("job.keyPath %s", keyPath)
	fmt.Printf("job.Project.Name %s", job.Project.Name)
	cmd := exec.Command("ansible-playbook", "--private-key", keyPath, "-i", job.Inventory.SourceFile, "-e", "@" + extraVarsFile.Name(), job.Playbook)
	cmd.Dir = fmt.Sprintf("storage/repositories/%s", job.Project.Name)
	cmd.Env = []string{"ANSIBLE_FORCE_COLOR=true"}
	processPipes(cmd, jobLogs, job)

	if err := cmd.Start(); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Cannot start command: %s", err))
	}

	job.Status = models.StatusCompleted
	if err := cmd.Wait(); err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Error waiting for process: %s", err))
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
	project := getProject(job)
	if project == nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("Project not found"))
		return fmt.Errorf("not found")
	}
	path := fmt.Sprintf("./storage/repositories/%s", project.Name)
	var repo *git.Repository
	var err error

	if project.SshKeyID == 0 && project.RepoUrl[0:4] != "http" {
		msg := "SSH key is required for SSH protocol"
		saveJobLog(jobLogs, job, msg)
		return fmt.Errorf(msg)
	}

	keys, err := getKey(project, jobLogs, job)
	fmt.Printf("PROJECT %#v\n", keys)
	if err != nil {
		return err
	}

	if _, err = os.Stat(path); os.IsNotExist(err) {
		repo, err = clone(project, jobLogs, keys, job)
	} else {
		repo, err = fetch(jobLogs, job, path, project, keys)
	}
	if err != nil {
		return err
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
	err = wTree.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: *hash})
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("git reset: %s", err))
		return err
	}

	saveJobLog(jobLogs, job, "Repository up to date")
	return nil
}

func getProject(job *models.Job) *models.Project {
	var projectID uint
	if job.ProjectID != 0 {
		projectID = job.ProjectID
	}
	if job.Application.ProjectID != 0 {
		projectID = job.Application.ProjectID
	}
	return models.GetProject(projectID)
}

func getKey(project *models.Project, jobLogs chan dto.Message, job *models.Job) (*ssh.PublicKeys, error) {
	if project.SshKeyID != 0 {
		keys, err := ssh.NewPublicKeys(project.RepoUser, []byte(project.SshKey.Key), "")
		if err != nil {
			saveJobLog(jobLogs, job, fmt.Sprintf("NewPublicKeys: %s", err))
			return nil, err
		}
		keys.HostKeyCallback = ssh2.InsecureIgnoreHostKey()
		return keys, nil
	}
	return nil, nil
}

func fetch(jobLogs chan dto.Message, job *models.Job, path string, project *models.Project, keys *ssh.PublicKeys) (*git.Repository, error) {
	saveJobLog(jobLogs, job, "git open")
	repo, err := git.PlainOpen(path)
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("git open: %s", err))
		return nil, err
	}
	saveJobLog(jobLogs, job, "git fetch")
	if project.SshKeyID != 0 {
		err = repo.Fetch(&git.FetchOptions{
			Progress:   utils.NewChanWriter(jobLogs),
			RemoteName: "origin",
			Auth:       keys,
		})
	} else {
		err = repo.Fetch(&git.FetchOptions{
			Progress:   utils.NewChanWriter(jobLogs),
			RemoteName: "origin",
		})
	}
	if err != nil && err.Error() != "already up-to-date" {
		saveJobLog(jobLogs, job, fmt.Sprintf("git fetch: %s", err))
		return nil, err
	}
	return repo, nil
}

func clone(project *models.Project, jobLogs chan dto.Message, keys *ssh.PublicKeys, job *models.Job) (*git.Repository, error) {
	saveJobLog(jobLogs, job, "git clone")
	var repo *git.Repository
	var err error
	if project.SshKeyID != 0 {
		repo, err = git.PlainClone(fmt.Sprintf("./storage/repositories/%s", project.Name), false, &git.CloneOptions{
			URL:      project.RepoUrl,
			Progress: utils.NewChanWriter(jobLogs),
			Auth:     keys,
		})
	} else {
		repo, err = git.PlainClone(fmt.Sprintf("./storage/repositories/%s", project.Name), false, &git.CloneOptions{
			URL:      project.RepoUrl,
			Progress: utils.NewChanWriter(jobLogs),
		})
	}
	if err != nil {
		saveJobLog(jobLogs, job, fmt.Sprintf("git clone: %s", err))
		return nil, err
	}
	return repo, err
}
