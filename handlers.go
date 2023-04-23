	package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"strings"
	"os"
//	"path/filepath"
	"log"
//	"encoding/json"
/*	"time"
	"fmt"  */
)

func messageContains(messageText, targetString string) bool {
	return strings.Contains(strings.ToLower(messageText), strings.ToLower(targetString))
}

func handleRemoveCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	removeSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/remove"))

	indexToRemove := -1
	for i, myResponse := range config.MyResponses {
		if myResponse.SearchPhrase == removeSearchPhrase {
			indexToRemove = i
			if myResponse.PhotoFilename != "" {
				err := os.Remove(myResponse.PhotoFilename)
				if err != nil {
					log.Printf("Error deleting photo: %v", err)
				}
			}
			if myResponse.GifFilename != "" {
				err := os.Remove(myResponse.GifFilename)
				if err != nil {
					log.Printf("Error deleting gif: %v", err)
				}
			}
			break
		}
	}

	if indexToRemove != -1 {
		config.MyResponses = append(config.MyResponses[:indexToRemove], config.MyResponses[indexToRemove+1:]...)

		// Save the updated config to the JSON file
		err := saveConfig("/home/longspear/chatwatchingbotconfig/config.json", *config)
		if err != nil {
			log.Printf("Error saving config: %v", err)
		}

		msg := tgbotapi.NewMessage(message.Chat.ID, "Response removed!")
		_, _ = bot.Send(msg)
	} else {
		msg := tgbotapi.NewMessage(message.Chat.ID, "No response found with that search phrase.")
		_, _ = bot.Send(msg)
	}

	return nil
}



func handleAddCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	newSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/add"))
	newResponse := message.ReplyToMessage.Text
	photoFileID := ""
	photoFilename := ""
	gifFileID := ""

	if len(message.ReplyToMessage.Photo) > 0 {
		photoFileID = message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
		newResponse = ""
		photoFilename, _ = downloadAndSavePhoto(bot, photoFileID, "/home/longspear/chatwatchingbot/photos")
	}
	if message.ReplyToMessage.Animation != nil {
		gifFileID := message.ReplyToMessage.Animation.FileID
		newResponse = ""
		gifFilename, _ := downloadAndSaveGif(bot, gifFileID, "/home/longspear/chatwatchingbot/gifs") // Replace this with your desired path
	}


	newMyResponse := MyResponse{
		SearchPhrase:  newSearchPhrase,
		Response:      newResponse,
		PhotoFileID:   photoFileID,
		PhotoFilename: photoFilename,
		GifFileID:     gifFileID,
		GifFilename:   gifFilename,
	}

	config.MyResponses = append(config.MyResponses, newMyResponse)

	// Save the updated config to the JSON file
	err := saveConfig("/home/longspear/chatwatchingbotconfig/config.json", *config)
	if err != nil {
		log.Printf("Error saving config: %v", err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)

	return nil
}

func processResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message, myResponse MyResponse) error {

	if message.Chat.Type != "supergroup" && message.Chat.Type != "group" {
		return nil
	}

	var msg tgbotapi.Chattable

	if (message.Chat.Type == "supergroup" || message.Chat.Type == "group") && myResponse.PhotoFilename != "" {
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FilePath(myResponse.PhotoFilename))
		photoMsg.ReplyToMessageID = message.MessageID
		msg = photoMsg
	} else if (message.Chat.Type == "supergroup" || message.Chat.Type == "group") && myResponse.GifFilename != "" {
		gifMsg := tgbotapi.NewAnimation(message.Chat.ID, tgbotapi.FilePath(myResponse.GifFilename))
		gifMsg.ReplyToMessageID = message.MessageID
		msg = gifMsg
	} else {
		if myResponse.Response == "" {
			return nil
		}
		textMsg := tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
		textMsg.ReplyToMessageID = message.MessageID
		msg = textMsg
	}

	_, err := bot.Send(msg)
	if err != nil {
		return err
	}

	return nil
}


func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	receivedMessage := message.Text

	if message.Command() == "add" && message.ReplyToMessage != nil {
		return handleAddCommand(bot, message, config)
	} else if message.Command() == "remove" {
		return handleRemoveCommand(bot, message, config)
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
