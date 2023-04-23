package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
//	"time"
)

type MyResponse struct {
	SearchPhrase      string    `json:"searchPhrase"`
	Response          string    `json:"response"`
	PhotoFileID  string `json:"photoFileID,omitempty"`
	PhotoFilename string `json:"photoFilename,omitempty"`
	GifFileID     string `json:"gifFileID,omitempty"`
	GifFilename   string `json:"gifFilename,omitempty"`
}

type Config struct {
	MyResponses    []MyResponse      `json:"myResponses"`
	ChatTriggers map[int64][]MyResponse  `json:"chat_triggers,omitempty"`
}

func main() {
	config, err := readConfig("/home/longspear/chatwatchingbotconfig/config.json")
	if err != nil {
		log.Panicf("Config error: %v", err)
	}


	token, err := readBotToken("/home/longspear/tokens/chatwatchingbot-token.txt")
	if err != nil {
		log.Panicf("Token error: ", err)
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60


	updates := bot.GetUpdatesChan(u)

	for update := range updates {

			if update.Message != nil {
				m := update.Message
				if m.From == nil {
					continue
				}

					log.Printf("Message received")
					err := handleMessage(bot, m, config)
					if err != nil {
						log.Printf("[%s] %s,   err: %s", update.Message.From.UserName, update.Message.Text, err.Error())
						continue
					}

				log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
			}
		}
	}
