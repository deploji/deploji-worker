package queue

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/sotomskir/mastermind-server/controllers"
	"github.com/sotomskir/mastermind-server/models"
	"github.com/sotomskir/mastermind-server/utils"
	"github.com/streadway/amqp"
	"log"
	"os"
	"os/exec"
	"time"
)

func handleError(err error, message string) {
	if err != nil {
		log.Fatalf("%s: %s", message, err)
	}
}

var ConnectQueue = func() {
	fmt.Println("processing queue")
	url := os.Getenv("AMQP_URL")
	conn, err := amqp.Dial(url)
	handleError(err, "Can't connect to AMQP")
	defer conn.Close()

	ch, err := conn.Channel()
	handleError(err, "Can't create a ch")

	defer ch.Close()

	queue, err := ch.QueueDeclare("deployments", true, false, false, false, nil)
	handleError(err, "Could not declare `deployments` queue")

	err = ch.Qos(1, 0, false)
	handleError(err, "Could not configure QoS")

	messageChannel, err := ch.Consume(
		queue.Name,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	handleError(err, "Could not register consumer")

	deploymentStatusExchangeName := "deployment_status"
	err = ch.ExchangeDeclare(
		deploymentStatusExchangeName,
		"fanout",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Error creating amqp exchange")
		return
	}

	stopChan := make(chan bool)

	go func() {
		log.Printf("Consumer ready, PID: %d", os.Getpid())
		for d := range messageChannel {
			log.Printf("Received a message: %s", d.Body)

			message := &models.Deployment{}

			err := json.Unmarshal(d.Body, message)
			if err != nil {
				log.Printf("Error decoding JSON: %s", err)
			}
			deployment := models.GetDeployment(message.ID)
			if deployment == nil {
				log.Printf("Deployment with ID: %d not found", message.ID)
				rejectMessage(d)
				return
			}
			err = models.UpdateDeploymentStatus(deployment, map[string]interface{}{"started_at": time.Now(), "status": models.StatusProcessing})
			sendStatusMessage(ch, deploymentStatusExchangeName, deployment.ID, deployment.Status)
			if err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Error saving deployment status: %s", err)})
				rejectMessage(d)
				return
			}

			logExchangeName := fmt.Sprintf("deployment_log_%d", deployment.ID)
			err = ch.ExchangeDeclare(
				logExchangeName,
				"fanout",
				true,
				false,
				false,
				false,
				nil,
			)
			if err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Error creating amqp exchange")})
				return
			}

			if err := controllers.SynchronizeProjectRepo(deployment.Application.ProjectID); err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Cannot synchronize project: %s", err)})
			}
			if err := utils.WriteKey(deployment.Inventory.Key.ID, deployment.Inventory.Key.Key); err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Cannot write key: %s", err)})
			}
			keyPath := fmt.Sprintf("storage/keys/%d", deployment.Inventory.Key.ID)
			version := fmt.Sprintf("version=%s", deployment.Version)
			app := fmt.Sprintf("app=%s", deployment.Application.AnsibleName)
			cmd := exec.Command("ansible-playbook", "--private-key", keyPath, "-i", deployment.Inventory.SourceFile, "-e", app, "-e", version, deployment.Application.AnsiblePlaybook)
			cmd.Dir = fmt.Sprintf("storage/repositories/%s", deployment.Application.Project.Name)
			cmd.Env = []string{"ANSIBLE_FORCE_COLOR=true"}
			cmdOutReader, err := cmd.StdoutPipe()
			if err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Cannot get stdout pipe: %s", err)})
			}
			outScanner := bufio.NewScanner(cmdOutReader)
			go func(ch *amqp.Channel, exchangeName string) {
				for outScanner.Scan() {
					models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: outScanner.Text()})
					sendLogMessage(ch, exchangeName, outScanner.Text())
					fmt.Println(outScanner.Text())
				}
			}(ch, logExchangeName)
			cmdErrReader, err := cmd.StderrPipe()
			if err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Cannot get stderr pipe: %s", err)})
			}
			errScanner := bufio.NewScanner(cmdErrReader)
			go func(ch *amqp.Channel, exchangeName string) {
				for errScanner.Scan() {
					models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: errScanner.Text()})
					sendLogMessage(ch, exchangeName, errScanner.Text())
					fmt.Println(errScanner.Text())
				}
			}(ch, logExchangeName)
			if err := cmd.Start(); err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Cannot start command: %s", err)})
			}
			if err := cmd.Wait(); err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Error waiting for process: %s", err)})
			}

			if cmd.ProcessState.ExitCode() != 0 {
				deployment.Status = models.StatusFailed
			} else {
				deployment.Status = models.StatusCompleted
			}

			err = models.UpdateDeploymentStatus(deployment, map[string]interface{}{"finished_at": time.Now(), "status": deployment.Status})
			if err != nil {
				models.SaveDeploymentLog(&models.DeploymentLog{Deployment: *deployment, Message: fmt.Sprintf("Error saving deployment status: %s", err)})
				rejectMessage(d)
				return
			}
			sendStatusMessage(ch, deploymentStatusExchangeName, deployment.ID, deployment.Status)
			if err := d.Ack(false); err != nil {
				log.Printf("Error acknowledging message : %s", err)
			} else {
				log.Printf("Acknowledged message")
			}
		}
	}()

	// Stop for program termination
	<-stopChan
}

func rejectMessage(d amqp.Delivery) {
	if err := d.Reject(false); err != nil {
		log.Printf("Error rejecting message: %s", err)
	} else {
		log.Printf("Rejected message")
	}
}

func sendStatusMessage(ch *amqp.Channel, exchangeName string, deploymentID uint, deploymentStatus models.Status) {
	type StatusMessage struct {
		Type   string
		ID     uint
		Status models.Status
	}
	body, err := json.Marshal(StatusMessage{Type: "StatusMessage", ID: deploymentID, Status: deploymentStatus})
	if err != nil {
		log.Printf("Error marshaling message: %s", err)
	}
	err = ch.Publish(
		exchangeName,
		"",
		false,
		false,
		amqp.Publishing{
			ContentType:  "text/plain",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		})
	if err != nil {
		log.Printf("Error sending status message: %s", err)
	}
}

func sendLogMessage(ch *amqp.Channel, exchangeName string, message string) {
	err := ch.Publish(
		exchangeName,
		"",
		false,
		false,
		amqp.Publishing{
			ContentType:  "text/plain",
			DeliveryMode: amqp.Persistent,
			Body:         []byte(message),
		})
	if err != nil {
		log.Printf("Error sending status message: %s", err)
	}
}
