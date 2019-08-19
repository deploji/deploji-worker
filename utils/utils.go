package utils

import (
	"fmt"
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
	if err := os.MkdirAll("storage/keys", os.ModePerm); err != nil {
		log.Printf("Error creating directory: %s", err)
		return err
	}
	if err := ioutil.WriteFile(fmt.Sprintf("storage/keys/%d", id), []byte(content), 0644); err != nil {
		log.Printf("Error saving key file: %s", err)
		return err
	}
	return nil
}
