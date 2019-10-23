package main

import (
	"fmt"
	"github.com/deploji/deploji-server/models"
	"github.com/deploji/deploji-worker/amqpService"
	"github.com/deploji/deploji-worker/handlers"
	"github.com/joho/godotenv"
	"golang.org/x/net/context"
	"os"
)

func main() {
	e := godotenv.Load()
	if e != nil {
		fmt.Print(e)
	}
	models.InitDatabase()
	ctx, done := context.WithCancel(context.Background())

	go func() {
		amqpService.Subscribe(amqpService.Redial(ctx, os.Getenv("AMQP_URL")), amqpService.Jobs, "jobs")
		done()
	}()

	go func() {
		amqpService.Publish(amqpService.Redial(ctx, os.Getenv("AMQP_URL")), amqpService.JobStatuses, "job_statuses")
		done()
	}()

	go func() {
		for job := range amqpService.Jobs {
			handlers.ProcessJobMessage(&job)
		}
	}()

	<-ctx.Done()
}
