package amqpService

import (
	"github.com/streadway/amqp"
	"os"
)

func CreateQueue(queue string) (*amqp.Connection, *amqp.Channel, error) {
	url := os.Getenv("AMQP_URL")
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, nil, err
	}
	channel, err := conn.Channel()
	if err != nil {
		return nil, nil, err
	}
	err = channel.Qos(1, 0, false)
	if err != nil {
		return nil, nil, err
	}
	if _, err := channel.QueueDeclare(queue, false, false, false, false, nil); err != nil {
		return nil, nil, err
	}
	return conn, channel, nil
}

func SendMessage(channel amqp.Channel, queue string, message string) error {
	err := channel.Publish("", queue, false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType: "text/plain",
		Body: []byte(message),
	})
	if err != nil {
		return err
	}
	return nil
}
