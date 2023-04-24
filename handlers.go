package main

import (
tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
"strings"
"os"
"log"
)

func messageContains(messageText, targetString string) bool {
return strings.Contains(strings.ToLower(messageText), strings.ToLower(targetString))
}

func handleRemoveCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
    log.Println("Handling /remove command")

    removeSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/remove"))

    chatTriggerRemoved := false
    chatTriggers, exists := config.ChatTriggers[message.Chat.ID]
    if exists {
        newChatTriggers := []MyResponse{}
        for _, myResponse := range chatTriggers {
            if myResponse.SearchPhrase != removeSearchPhrase {
                newChatTriggers = append(newChatTriggers, myResponse)
            } else {
                chatTriggerRemoved = true

                // Add file deletion for ChatTriggers
                if myResponse.FileType != "" {
                    err := os.Remove(myResponse.Filename)
                    if err != nil {
                        log.Printf("Error deleting media: %v", err)
                    }
                }
            }
        }
        config.ChatTriggers[message.Chat.ID] = newChatTriggers
    }

    err := saveConfig("./config/config.json", *config)
    if err != nil {
        log.Printf("Error saving config: %v", err)
    }

    if chatTriggerRemoved {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Local response removed!")
        _, _ = bot.Send(msg)
    } else {
        msg := tgbotapi.NewMessage(message.Chat.ID, "No local response found with that search phrase.")
        _, _ = bot.Send(msg)
    }

    return nil
}

func handleRemoveGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	log.Println("Handling /removeglobal command")

	// Check if the message comes from the allowed user
	if message.From.ID != int64(193117018) {
		msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
		_, _ = bot.Send(msg)
		return nil
	}

	removeSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/removeglobal"))

	globalTriggerRemoved := false
	newMyResponses := []MyResponse{}
	for _, myResponse := range config.MyResponses {
		if myResponse.SearchPhrase != removeSearchPhrase {
			newMyResponses = append(newMyResponses, myResponse)
		} else {
			globalTriggerRemoved = true

			// Add file deletion for GlobalTriggers
			if myResponse.FileType != "" {
				err := os.Remove(myResponse.Filename)
				if err != nil {
					log.Printf("Error deleting media: %v", err)
				}
			}
		}
	}
	config.MyResponses = newMyResponses

	err := saveConfig("./config/config.json", *config)
	if err != nil {
		log.Printf("Error saving config: %v", err)
	}

	if globalTriggerRemoved {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Global response removed!")
		_, _ = bot.Send(msg)
	} else {
		msg := tgbotapi.NewMessage(message.Chat.ID, "No global response found with that search phrase.")
		_, _ = bot.Send(msg)
	}

	return nil
}



func handleAddCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	newSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/add"))

	newMyResponse, err := createMyResponse(bot, message)
	if err != nil {
			log.Printf("Error creating MyResponse: %v", err)
			return err
	}
	newMyResponse.SearchPhrase = newSearchPhrase

	updateConfig(config, message, newMyResponse)

	err = saveConfig("./config/config.json", *config)
	if err != nil {
			log.Printf("Error saving config: %v", err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)

	return nil
}

func handleAddGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	newSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/addglobal"))

	newMyResponse, err := createMyResponse(bot, message)
	if err != nil {
			log.Printf("Error creating MyResponse: %v", err)
			return err
	}
	newMyResponse.SearchPhrase = newSearchPhrase

	updateGlobalConfig(config, message, newMyResponse)

	err = saveConfig("./config/config.json", *config)
	if err != nil {
			log.Printf("Error saving config: %v", err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)

	return nil
}

func updateGlobalConfig(config *Config, message *tgbotapi.Message, myResponse MyResponse) {
			config.MyResponses = append(config.MyResponses, myResponse)
}


func createMyResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message) (MyResponse, error) {
	var myResponse MyResponse

	if message.ReplyToMessage.Caption != "" {
		myResponse.Response = message.ReplyToMessage.Caption
	} else {
		myResponse.Response = message.ReplyToMessage.Text
	}

	if len(message.ReplyToMessage.Photo) > 0 {
		photoFileID := message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
		Filename, err := downloadAndSaveFile(bot, photoFileID, "./photos", ".jpg")
		if err != nil {
			return myResponse, err
		}
		myResponse.FileType = "photo"
		myResponse.FileID = photoFileID
		myResponse.Filename = Filename
	} else if message.ReplyToMessage.Animation != nil {
		gifFileID := message.ReplyToMessage.Animation.FileID
		Filename, err := downloadAndSaveFile(bot, gifFileID, "./gifs", ".gif")
		if err != nil {
			return myResponse, err
		}
		myResponse.FileType = "gif"
		myResponse.FileID = gifFileID
		myResponse.Filename = Filename
	} else if message.ReplyToMessage.Voice != nil { // Add this block
		voiceFileID := message.ReplyToMessage.Voice.FileID
		Filename, err := downloadAndSaveFile(bot, voiceFileID, "./voices", ".ogg")
		if err != nil {
			return myResponse, err
		}
		myResponse.FileType = "voice"
		myResponse.FileID = voiceFileID
		myResponse.Filename = Filename
	} else if message.ReplyToMessage.Sticker != nil { // Add this block
        stickerFileID := message.ReplyToMessage.Sticker.FileID
        Filename, err := downloadAndSaveFile(bot, stickerFileID, "./stickers", ".webp")
        if err != nil {
            return myResponse, err
        }
        myResponse.FileType = "sticker"
        myResponse.FileID = stickerFileID
        myResponse.Filename = Filename
    } else {
		log.Println("Unsupported message type: ", message)
	}

	return myResponse, nil
}



func updateConfig(config *Config, message *tgbotapi.Message, myResponse MyResponse) {
	if message.Chat.Type == "supergroup" || message.Chat.Type == "group" {
			if config.ChatTriggers == nil {
					config.ChatTriggers = make(map[int64][]MyResponse)
			}
			config.ChatTriggers[message.Chat.ID] = append(config.ChatTriggers[message.Chat.ID], myResponse)
	} else {
			config.MyResponses = append(config.MyResponses, myResponse)
	}
}


func processResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message, myResponse MyResponse) error {
	if myResponse.Response == "" && myResponse.FileType == "gif" && myResponse.FileType == "photo" {
			return nil
	}

	chattableResponse, err := buildChattableResponse(message, myResponse)
	if err != nil {
			return err
	}

	_, err = bot.Send(chattableResponse)
	if err != nil {
			return err
	}

	return nil
}

func buildChattableResponse(message *tgbotapi.Message, myResponse MyResponse) (tgbotapi.Chattable, error) {
	if myResponse.FileType == "photo" {
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FilePath(myResponse.Filename))
		photoMsg.ReplyToMessageID = message.MessageID
		photoMsg.Caption = myResponse.Response
		return photoMsg, nil
	} else if myResponse.FileType == "gif" {
		gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FilePath(myResponse.Filename))
		gifMsg.ReplyToMessageID = message.MessageID
		gifMsg.Caption = myResponse.Response
		return gifMsg, nil
	} else if myResponse.FileType == "voice" {
		voiceMsg := tgbotapi.NewVoice(message.Chat.ID, tgbotapi.FilePath(myResponse.Filename))
		voiceMsg.ReplyToMessageID = message.MessageID
		voiceMsg.ReplyToMessageID = message.MessageID
		return voiceMsg, nil
	} else if myResponse.FileType == "sticker" {
    stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FilePath(myResponse.Filename))
    stickerMsg.ReplyToMessageID = message.MessageID
    return stickerMsg, nil
    } else {
		textMsg := tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
		textMsg.ReplyToMessageID = message.MessageID
		return textMsg, nil
	}
}




type commandHandlerFunc func(*tgbotapi.BotAPI, *tgbotapi.Message, *Config) error

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	receivedMessage := message.Text

	if message.Chat.Type != "supergroup" && message.Chat.Type != "group" {
		return nil
	}

	commandHandlers := map[string]commandHandlerFunc{
		"add":         handleAddCommand,
		"remove":      handleRemoveCommand,
		"addglobal":   handleAddGlobalCommand,
		"triggers":    handleTriggersCommand,
		"removeglobal": handleRemoveGlobalCommand,
	}

	command := message.Command()
	if handler, ok := commandHandlers[command]; ok {
		allowedCommands := map[string]bool{
			"removeglobal": true,
			"triggers":     true,
			"remove":       true,
		}

		if allowed, _ := allowedCommands[command]; allowed || message.ReplyToMessage != nil {
			return handler(bot, message, config)
		}
	}

	chatSpecificTriggerFound := false
	if message.Chat.Type == "supergroup" || message.Chat.Type == "group" {
		chatTriggers, exists := config.ChatTriggers[message.Chat.ID]
		if exists {
			for _, myResponse := range chatTriggers {
				if messageContains(receivedMessage, myResponse.SearchPhrase) {
					err := processResponse(bot, message, myResponse)
					if err != nil {
						return err
					}
					chatSpecificTriggerFound = true
					break
				}
			}
		}
	}

	if !chatSpecificTriggerFound {
		for _, myResponse := range config.MyResponses {
			if messageContains(receivedMessage, myResponse.SearchPhrase) {
				err := processResponse(bot, message, myResponse)
				if err != nil {
					return err
				}
				break
			}
		}
	}

	return nil
}


func handleTriggersCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
	log.Println("Handling /triggers command")

	chatTriggers, exists := config.ChatTriggers[message.Chat.ID]
	var localTriggers, globalTriggers []string

	if exists {
		for _, myResponse := range chatTriggers {
			localTriggers = append(localTriggers, myResponse.SearchPhrase)
		}
	}

	for _, myResponse := range config.MyResponses {
		globalTriggers = append(globalTriggers, myResponse.SearchPhrase)
	}

	localTriggersStr := strings.Join(localTriggers, ", ")
	globalTriggersStr := strings.Join(globalTriggers, ", ")

	response := "Local Triggers:\n" + localTriggersStr + "\n\nGlobal Triggers:\n" + globalTriggersStr

	msg := tgbotapi.NewMessage(message.Chat.ID, response)
	_, _ = bot.Send(msg)

	return nil
}
