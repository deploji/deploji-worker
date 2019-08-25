package utils

import (
	"fmt"
	"github.com/sotomskir/mastermind-server/dto"
	"io/ioutil"
	"log"
	"os"
)

func HandleError(err error, message string) {
	if err != nil {
		log.Fatalf("%s: %s", message, err)
	}
}

func WriteKey(id uint, content string) error {
	if err := os.MkdirAll("storage/keys", 0700); err != nil {
		log.Printf("Error creating directory: %s", err)
		return err
	}
	if err := ioutil.WriteFile(fmt.Sprintf("storage/keys/%d", id), []byte(content), 0700); err != nil {
		log.Printf("Error saving key file: %s", err)
		return err
	}
	return nil
}

type chanWriter struct {
	ch chan dto.Message
}

func NewChanWriter(ch chan dto.Message) *chanWriter {
	return &chanWriter{ch}
}

func (w *chanWriter) Chan() <-chan dto.Message {
	return w.ch
}

func (w *chanWriter) Write(p []byte) (int, error) {
	w.ch <-p
	return len(p), nil
}

func (w *chanWriter) Close() error {
	close(w.ch)
	return nil
}
