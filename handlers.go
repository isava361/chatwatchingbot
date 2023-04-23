	package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strings"
//	"path/filepath"
	"log"
//	"encoding/json"
/*	"time"
	"fmt"  */
)

func messageContains(messageText, targetString string) bool {
	return strings.Contains(strings.ToLower(messageText), strings.ToLower(targetString))
}

func handleAddCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	newSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/add"))
	newResponse := message.ReplyToMessage.Text
	photoFileID := ""
	photoFilename := ""

	if len(message.ReplyToMessage.Photo) > 0 {
		photoFileID = message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
		newResponse = ""
		photoFilename, _ = downloadAndSavePhoto(bot, photoFileID, "/home/longspear/chatwatchingbot/photos")
	}

	newMyResponse := MyResponse{
		SearchPhrase:  newSearchPhrase,
		Response:      newResponse,
		PhotoFileID:   photoFileID,
		PhotoFilename: photoFilename,
	}

	config.MyResponses = append(config.MyResponses, newMyResponse)

	// Save the updated config to the JSON file
	err := saveConfig("config.json", *config)
	if err != nil {
		log.Printf("Error saving config: %v", err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)

	return nil
}

func processResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message, myResponse MyResponse) error {
	var msg tgbotapi.Chattable

	if (message.Chat.Type == "supergroup" || message.Chat.Type == "group") && myResponse.PhotoFilename != "" {
		msg = tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FilePath(myResponse.PhotoFilename))
	} else {
		if myResponse.Response == "" {
			return nil
		}
		msg = tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
	}

	msg.ReplyToMessageID = message.MessageID
	_, err := bot.Send(msg)
	if err != nil {
		return err
	}

	return nil
}


func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	receivedMessage := message.Text

	if message.ReplyToMessage != nil && message.Command() == "add" {
		return handleAddCommand(bot, message, config)
	}

	for _, myResponse := range config.MyResponses {
		if messageContains(receivedMessage, myResponse.SearchPhrase) {
			err := processResponse(bot, message, myResponse)
			if err != nil {
				return err
			}
			break
		}
	}

	return nil
}
