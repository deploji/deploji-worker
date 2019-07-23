package queue

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Shopify/sarama"
	"github.com/sotomskir/mastermind-server/models"
	"github.com/streadway/amqp"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
)

var (
	certFile  = flag.String("certificate", "", "The optional certificate file for client authentication")
	keyFile   = flag.String("key", "", "The optional key file for client authentication")
	caFile    = flag.String("ca", "", "The optional certificate authority file for TLS client authentication")
	verifySsl = flag.Bool("verify", false, "Optional verify ssl certificates chain")
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
			deployment.Status = models.StatusProcessing
			err = models.UpdateDeploymentStatus(deployment)
			sendStatusMessage(ch, deploymentStatusExchangeName, deployment.ID, deployment.Status)
			if err != nil {
				log.Printf("Error saving deployment status: %s", err)
				rejectMessage(d)
				return
			}

			//dataCollector := newDataCollector(strings.Split(os.Getenv("KAFKA_PEERS"), ","))
			//defer dataCollector.Close()
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
				log.Printf("Error creating amqp exchange")
				return
			}

			//sendLogMessage(dataCollector, fmt.Sprintf("deployment_log_%d", deployment.ID), fmt.Sprintf("log %d\n", i))

			//time.Sleep(5 * time.Second)
			cmd := exec.Command("ansible-playbook", "-i", deployment.Inventory.SourceFile, deployment.Application.AnsiblePlaybook)
			cmd.Dir = fmt.Sprintf("/home/sotomski/go/src/github.com/sotomskir/mastermind-server/storage/repositories/%s", deployment.Application.Project.Name)
			cmdOutReader, err := cmd.StdoutPipe()
			if err != nil {
				log.Printf("Cannot get stdout pipe: %s", err)
			}
			outScanner := bufio.NewScanner(cmdOutReader)
			go func(ch *amqp.Channel, exchangeName string) {
				for outScanner.Scan() {
					if err := models.SaveDeploymentLog(&models.DeploymentLog{Deployment:*deployment, Message:outScanner.Text()}); err != nil {
						log.Printf("Cannot save log: %s", err)
					}
					sendLogMessage(ch, exchangeName, outScanner.Text())
					fmt.Println(outScanner.Text())
				}
			}(ch, logExchangeName)
			cmdErrReader, err := cmd.StderrPipe()
			if err != nil {
				log.Printf("Cannot get stderr pipe: %s", err)
			}
			errScanner := bufio.NewScanner(cmdErrReader)
			go func(ch *amqp.Channel, exchangeName string) {
				for errScanner.Scan() {
					if err := models.SaveDeploymentLog(&models.DeploymentLog{Deployment:*deployment, Message:errScanner.Text()}); err != nil {
						log.Printf("Cannot save log: %s", err)
					}
					sendLogMessage(ch, exchangeName, errScanner.Text())
					fmt.Println(errScanner.Text())
				}
			}(ch, logExchangeName)
			if err := cmd.Start(); err != nil {
				log.Printf("Cannot start command: %s", err)
			}
			if err := cmd.Wait(); err != nil {
				log.Printf("Error waiting for process: %s", err)
			}

			if cmd.ProcessState.ExitCode() != 0 {
				deployment.Status = models.StatusFailed
			} else {
				deployment.Status = models.StatusCompleted
			}

			err = models.UpdateDeploymentStatus(deployment)
			if err != nil {
				log.Printf("Error saving deployment status: %s", err)
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

func sendLogMessage1(producer sarama.SyncProducer, topic string, message string) {
	_, _, err := producer.SendMessage(&sarama.ProducerMessage{
		Topic: topic,
		Value: sarama.StringEncoder(message),
	})
	if err != nil {
		log.Printf("Error sending log message: %s", err)
	}
}

func newDataCollector(brokerList []string) sarama.SyncProducer {
	//sarama.Logger = log.New(os.Stdout, "sarma", 0)
	// For the data collector, we are looking for strong consistency semantics.
	// Because we don't change the flush settings, sarama will try to produce messages
	// as fast as possible to keep latency low.
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll // Wait for all in-sync replicas to ack the message
	config.Producer.Retry.Max = 10                   // Retry up to 10 times to produce the message
	config.Producer.Return.Successes = true
	tlsConfig := createTlsConfiguration()
	if tlsConfig != nil {
		config.Net.TLS.Config = tlsConfig
		config.Net.TLS.Enable = true
	}

	// On the broker side, you may want to change the following settings to get
	// stronger consistency guarantees:
	// - For your broker, set `unclean.leader.election.enable` to false
	// - For the topic, you could increase `min.insync.replicas`.
	fmt.Println("brokerList")
	fmt.Println(brokerList)
	producer, err := sarama.NewSyncProducer(brokerList, config)
	if err != nil {
		log.Fatalln("Failed to start Sarama producer:", err)
	}

	return producer
}

func createTlsConfiguration() (t *tls.Config) {
	if *certFile != "" && *keyFile != "" && *caFile != "" {
		cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			log.Fatal(err)
		}

		caCert, err := ioutil.ReadFile(*caFile)
		if err != nil {
			log.Fatal(err)
		}

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		t = &tls.Config{
			Certificates:       []tls.Certificate{cert},
			RootCAs:            caCertPool,
			InsecureSkipVerify: *verifySsl,
		}
	}
	// will be nil by default if nothing is provided
	return t
}
