package webPushService

import (
	"encoding/json"
	"github.com/SherClockHolmes/webpush-go"
	"github.com/deploji/deploji-server/models"
	"github.com/sirupsen/logrus"
	"log"
)

type NotificationPayload struct {
	Notification Notification `json:"notification"`
}

type Notification struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Icon    string `json:"icon"`
	Vibrate []uint `json:"vibrate"`
	Data    NotificationData `json:"data"`
	Actions []Action `json:"actions"`
}

type NotificationData struct {
	DateOfArrival string `json:"dateOfArrival"`
	PrimaryKey    uint `json:"primaryKey"`
}

type Action struct {
	Action string `json:"action"`
	Title  string `json:"title"`
}

func Send(sub string, title string, text string) error {
	logrus.Infof( "sub: %v\n", sub)
	s := &webpush.Subscription{}
	err := json.Unmarshal([]byte(sub), s)
	if err != nil {
		logrus.Errorf("Error Unmarshal Subscription: %v", err)
		return err
	}
	logrus.Infof( "s: %v\n", s)
	payload := NotificationPayload{
		Notification: Notification{
			Title:   title,
			Body:    text,
			Icon:    "",
			Vibrate: []uint{100, 50, 100},
			Data:    NotificationData{
				DateOfArrival: "2020-01-01",
				PrimaryKey:    1,
			},
			Actions: []Action{
				{
					Action: "explore",
					Title:  "Go to site",
				},
			},
		},
	}
	message, err := json.Marshal(&payload)
	if err != nil {
		log.Printf("Error marshaling payload: %v", payload)
		return err
	}
	privateKey := models.GetSettingValue("PUSH", "privateKey", "")
	publicKey := models.GetSettingValue("PUSH", "publicKey", "")
	logrus.Infof("privateKey: %s, publicKey: %s", privateKey, publicKey)
	// Send Notification
	resp, err := webpush.SendNotification(message, s, &webpush.Options{
		Subscriber:      "example@example.com",
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
		TTL:             30,
	})
	if err != nil || resp.StatusCode != 201 {
		logrus.Errorf("Error executing webPush: %#v: \nerr: %#v\n payload: %#v\nresponse: %#v", s, err, payload, resp)
		return err
	} else {
		logrus.Infof("webPush sent, response: %v", resp)
	}
	defer resp.Body.Close()
	return nil
}
