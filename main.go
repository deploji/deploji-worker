package main

import (
	"fmt"
	"github.com/joho/godotenv"
	"github.com/sotomskir/mastermind-worker/queue"
)

func main() {
	e := godotenv.Load()
	if e != nil {
		fmt.Print(e)
	}
	queue.ConnectQueue()
}
