package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
//	"time"
)

//type TempChat struct {
//	ChatName      string    `json:"chatName"`
//	TimeAdded     int       `json:"timeAdded"` // or you can use time.Duration if you want to store it as a duration instead of a string
//	DateTimeAdded time.Time `json:"dateTimeAdded"`
//}
//
//type Config struct {
//	AllowedUser  int64           `json:"allowedUser"`
//	AllowedUsers map[int64]bool  `json:"allowedUsers"`
//	Whitelist    map[string]bool `json:"whitelist"`
//	AllowedChats map[int64]bool  `json:"allowedChats"`
//	TempChats    []TempChat      `json:"tempChats"`
//}

func main() {
//	config, err := readConfig("/home/longspear/tokens/test-config.json")
//	if err != nil {
//		log.Panicf("Config error: %v", err)
//	}

//	allowedChats := config.AllowedChats
//	whitelist := config.Whitelist

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

//	go checkTempChats(bot, &config)

	updates := bot.GetUpdatesChan(u)

	for update := range updates {

			if update.Message != nil {
				m := update.Message
				if m.From == nil {
					continue
				}

					log.Printf("Message received")
					err := handleMessage(bot, m)
					if err != nil {
						log.Printf("[%s] %s,   err: %s", update.Message.From.UserName, update.Message.Text, err.Error())
						continue
					}

				log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
			}
		}
	}
