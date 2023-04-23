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
			}
		}
		config.ChatTriggers[message.Chat.ID] = newChatTriggers
	}

	if !chatTriggerRemoved {
			newMyResponses := []MyResponse{}
			for _, myResponse := range config.MyResponses {
					if myResponse.SearchPhrase != removeSearchPhrase {
							newMyResponses = append(newMyResponses, myResponse)
					} else {
							chatTriggerRemoved = true
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
					}
			}
			config.MyResponses = newMyResponses
	}

	err := saveConfig("/home/longspear/chatwatchingbotconfig/config.json", *config)
	if err != nil {
			log.Printf("Error saving config: %v", err)
	}

	if chatTriggerRemoved {
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

	newMyResponse, err := createMyResponse(bot, message)
	if err != nil {
			log.Printf("Error creating MyResponse: %v", err)
			return err
	}
	newMyResponse.SearchPhrase = newSearchPhrase

	updateConfig(config, message, newMyResponse)

	err = saveConfig("/home/longspear/chatwatchingbotconfig/config.json", *config)
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

	err = saveConfig("/home/longspear/chatwatchingbotconfig/config.json", *config)
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
    myResponse.Response = message.ReplyToMessage.Text

    if len(message.ReplyToMessage.Photo) > 0 {
        photoFileID := message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
        photoFilename, err := downloadAndSaveFile(bot, photoFileID, "/home/longspear/chatwatchingbot/photos",".jpg")
        if err != nil {
            return myResponse, err
        }
        myResponse.Response = ""
        myResponse.PhotoFilename = photoFilename
        myResponse.PhotoFileID = photoFileID
    } else if message.ReplyToMessage.Animation != nil || message.ReplyToMessage.Video != nil {
        gifFileID := message.ReplyToMessage.Animation.FileID
        gifFilename, err := downloadAndSaveFile(bot, gifFileID, "/home/longspear/chatwatchingbot/gifs",".gif")
        if err != nil {
            return myResponse, err
        }
        myResponse.Response = ""
        myResponse.GifFilename = gifFilename
        myResponse.GifFileID = gifFileID
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
	if myResponse.Response == "" && myResponse.PhotoFilename == "" && myResponse.GifFilename == "" {
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
	if myResponse.PhotoFilename != "" {
			photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FilePath(myResponse.PhotoFilename))
			photoMsg.ReplyToMessageID = message.MessageID
			return photoMsg, nil
	} else if myResponse.GifFilename != "" {
			gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FilePath(myResponse.GifFilename))
			gifMsg.ReplyToMessageID = message.MessageID
			return gifMsg, nil
	} else {
			textMsg := tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
			textMsg.ReplyToMessageID = message.MessageID
			return textMsg, nil
	}
}

/*func handleAddGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
    authorizedUserID := int64(193117018)
    if message.From.ID != authorizedUserID {
        msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
        _, _ = bot.Send(msg)
        return nil
    }

    newSearchPhrase := strings.TrimSpace(strings.TrimPrefix(message.Text, "/addglobal"))

    if message.ReplyToMessage == nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Please reply to a message containing text, photo, or gif to use this command.")
        _, _ = bot.Send(msg)
        return nil
    }

    newMyResponse, err := createMyResponse(bot, message.ReplyToMessage)
    if err != nil {
        log.Printf("Error creating MyResponse: %v", err)
        return err
    }
    newMyResponse.SearchPhrase = newSearchPhrase

    updateGlobalConfig(config, newMyResponse)

    err = saveConfig("/home/longspear/chatwatchingbotconfig/config.json", *config)
    if err != nil {
        log.Printf("Error saving config: %v", err)
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, "New global response added!")
    _, _ = bot.Send(msg)

    return nil
}
*/



type commandHandlerFunc func(*tgbotapi.BotAPI, *tgbotapi.Message, *Config) error

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config) error {
    receivedMessage := message.Text

    if message.Chat.Type != "supergroup" && message.Chat.Type != "group" {
        return nil
    }

    commandHandlers := map[string]commandHandlerFunc{
        "add":       handleAddCommand,
        "remove":    handleRemoveCommand,
        "addglobal": handleAddGlobalCommand,
    }

    if handler, ok := commandHandlers[message.Command()]; ok {
        if message.Command() == "remove" || message.ReplyToMessage != nil {
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
