package utils

import (
	"fmt"
	"github.com/deploji/deploji-server/dto"
	"io/ioutil"
	"log"
	"os"
	"strings"
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

func AnsiColor(s string) string {
	s = strings.ReplaceAll(s, "\u001b[0;30m", "<span class=\"ansi-black\">")
	s = strings.ReplaceAll(s, "\u001b[1;30m", "<span class=\"ansi-bright-black\">")
	s = strings.ReplaceAll(s, "\u001b[0;31m", "<span class=\"ansi-red\">")
	s = strings.ReplaceAll(s, "\u001b[1;31m", "<span class=\"ansi-bright-red\">")
	s = strings.ReplaceAll(s, "\u001b[0;32m", "<span class=\"ansi-green\">")
	s = strings.ReplaceAll(s, "\u001b[1;32m", "<span class=\"ansi-bright-green\">")
	s = strings.ReplaceAll(s, "\u001b[0;33m", "<span class=\"ansi-yellow\">")
	s = strings.ReplaceAll(s, "\u001b[1;33m", "<span class=\"ansi-bright-yellow\">")
	s = strings.ReplaceAll(s, "\u001b[0;34m", "<span class=\"ansi-blue\">")
	s = strings.ReplaceAll(s, "\u001b[1;34m", "<span class=\"ansi-bright-blue\">")
	s = strings.ReplaceAll(s, "\u001b[0;35m", "<span class=\"ansi-magenta\">")
	s = strings.ReplaceAll(s, "\u001b[1;35m", "<span class=\"ansi-bright-magenta\">")
	s = strings.ReplaceAll(s, "\u001b[0;36m", "<span class=\"ansi-cyan\">")
	s = strings.ReplaceAll(s, "\u001b[1;36m", "<span class=\"ansi-bright-cyan\">")
	s = strings.ReplaceAll(s, "\u001b[0;37m", "<span class=\"ansi-white\">")
	s = strings.ReplaceAll(s, "\u001b[1;37m", "<span class=\"ansi-bright-white\">")
	s = strings.ReplaceAll(s, "\u001b[0m", "</span>")
	s = strings.ReplaceAll(s, "\n", "<br/>")
	s = strings.ReplaceAll(s, "\t", "&nbsp&nbsp&nbsp&nbsp")
	return s
}
