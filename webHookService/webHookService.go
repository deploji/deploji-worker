package webHookService

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
)

func Send(webHook string, title string, text string) {
	payload := fmt.Sprintf("{\"text\": \"%s\n%s\"}", title, text)
	resp, err := http.Post(webHook, "application/json", bytes.NewBufferString(payload))
	if err != nil || resp.StatusCode >= 400 {
		log.Printf("Error executing webHook: %s: %s, payload: %s\nresponse: %v", webHook, err, payload, resp)
	}
}
