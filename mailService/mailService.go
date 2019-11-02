package mailService

import (
	"crypto/tls"
	"github.com/deploji/deploji-server/models"
	"gopkg.in/gomail.v2"
	"log"
	"strconv"
	"strings"
)

func Send(recipients string, subject string, body string) error {
	host := models.GetSettingValue("SMTP", "host", "localhost")
	port, err := strconv.ParseInt(models.GetSettingValue("SMTP", "port", "1025"), 10, 16)
	if err != nil {
		log.Println(err)
		port = 1025
	}
	from := models.GetSettingValue("SMTP", "sender", "noreply@deploji.com")
	username := models.GetSettingValue("SMTP", "username", "")
	password := models.GetSettingValue("SMTP", "password", "")

	for _, to := range strings.Split(recipients, ",") {
		m := gomail.NewMessage()
		m.SetHeader("From", from)
		m.SetHeader("To", to)
		m.SetHeader("Subject", subject)
		m.SetBody("text/html", body)

		d := gomail.NewDialer(host, int(port), username, password)
		d.TLSConfig = &tls.Config{InsecureSkipVerify: true}
		if err := d.DialAndSend(m); err != nil {
			log.Println(err)
			return err
		}
	}
	return nil
}
