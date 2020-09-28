package amqpService

import (
	"github.com/deploji/deploji-server/dto"
	"github.com/streadway/amqp"
	"golang.org/x/net/context"
	"log"
)

var (
	Jobs        = make(chan dto.Message)
	JobStatuses = make(chan dto.Message)
)

// session composes an amqp.Connection with an amqp.Channel
type session struct {
	*amqp.Connection
	*amqp.Channel
}

// Close tears the connection down, taking the channel with it.
func (s session) Close() error {
	if s.Connection == nil {
		return nil
	}
	return s.Connection.Close()
}

// redial continually connects to the URL
func Redial(ctx context.Context, url string) chan chan session {
	sessions := make(chan chan session)

	go func() {
		sess := make(chan session)
		defer close(sessions)

		for {
			select {
			case sessions <- sess:
			case <-ctx.Done():
				log.Println("shutting down session factory")
				return
			}

			conn, err := amqp.Dial(url)
			if err != nil {
				log.Fatalf("cannot (re)dial: %v: %q", err, url)
			}

			ch, err := conn.Channel()
			if err != nil {
				log.Fatalf("cannot create channel: %v", err)
			}

			select {
			case sess <- session{conn, ch}:
			case <-ctx.Done():
				log.Println("shutting down new session")
				return
			}
		}
	}()

	return sessions
}

// publish publishes messages to a reconnecting session to a fanout exchange.
// It receives from the application specific source of messages.
func Publish(sessions chan chan session, messages <-chan dto.Message, exchangeName string) {
	for session := range sessions {
		var (
			running bool
			reading = messages
			pending = make(chan dto.Message, 1)
			confirm = make(chan amqp.Confirmation, 1)
		)

		pub := <-session

		if err := pub.Channel.ExchangeDeclare(exchangeName, "fanout", true, false, false, false, nil); err != nil {
			log.Printf("cannot declare fanout exchange: %v", err)
		}

		// publisher confirms for this channel/connection
		if err := pub.Confirm(false); err != nil {
			log.Printf("publisher confirms not supported")
			close(confirm) // confirms not supported, simulate by always nacking
		} else {
			pub.NotifyPublish(confirm)
		}

		log.Printf("publishing exchange %s", exchangeName)

	Publish:
		for {
			var body dto.Message
			select {
			case confirmed, ok := <-confirm:
				if !ok {
					break Publish
				}
				if !confirmed.Ack {
					log.Printf("nack message %d, body: %q", confirmed.DeliveryTag, string(body))
				}
				reading = messages

			case body = <-pending:
				routingKey := "ignored for fanout exchanges, application dependent for other exchanges"
				err := pub.Publish(exchangeName, routingKey, false, false, amqp.Publishing{
					Body: body,
				})
				// Retry failed delivery on the next session
				if err != nil {
					pending <- body
					pub.Close()
					break Publish
				}

			case body, running = <-reading:
				// all messages consumed
				if !running {
					return
				}
				// work on pending delivery until ack'd
				pending <- body
				reading = nil
			}
		}
	}
}

func Subscribe(sessions chan chan session, messages chan<- dto.Message, queueName string) {
	for session := range sessions {
		sub := <-session

		if _, err := sub.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
			log.Printf("cannot consume from queue: %s, %s", queueName, err)
			return
		}

		if err := sub.Channel.Qos(1, 0, false); err != nil {
			log.Printf("Could not configure QoS: %s, %s", queueName, err)
			return
		}

		deliveries, err := sub.Consume(queueName, "", false, false, false, false, nil)
		if err != nil {
			log.Printf("cannot consume from: %s, %s", queueName, err)
			return
		}

		log.Printf("subscribed queue %s", queueName)

		for msg := range deliveries {
			messages <- msg.Body
			sub.Ack(msg.DeliveryTag, false)
		}
	}
}
