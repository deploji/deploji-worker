package templates

import (
	"bytes"
	"github.com/deploji/deploji-server/models"
	"github.com/deploji/deploji-worker/utils"
	"text/template"
	"time"
)

type NotificationType string

const (
	NotificationTypeSuccess NotificationType = "success"
	NotificationTypeFail    NotificationType = "fail"
)

type NotificationEmailTemplate struct {
	Title       string
	Type        NotificationType
	JobType     string
	Inventory   string
	Application string
	Version     string
	User        string
	JobID       string
	JobStart    time.Time
	JobLogs     []*models.JobLog
}

func (t NotificationEmailTemplate) Html() string {
	tmpl := template.Must(template.ParseFiles("./templates/notification.email.html"))
	var buffer bytes.Buffer
	for _, l := range t.JobLogs {
		l.Message = utils.AnsiColor(l.Message)
	}
	if err := tmpl.Execute(&buffer, t); err != nil {
		panic(err)
	}
	return buffer.String()
}

