package main

import (
tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
"strings"
"os"
"log"
"fmt"
"github.com/fsnotify/fsnotify"
"strconv"
"github.com/skip2/go-qrcode"
"github.com/boombuler/barcode"
"github.com/boombuler/barcode/code128"
"image/png"
"database/sql"
)



func allowedMessageType(message *tgbotapi.Message) bool {
	if (message.ReplyToMessage.Game != nil){
		return false
	}
	return true
}

func messageContains(messageText, targetString string) bool {
return strings.Contains(strings.ToLower(messageText), strings.ToLower(targetString))
}

func handleRemoveCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config, configwriter ConfigWriter) error {
    log.Println("Handling /remove command")

    removeSearchPhrase := message.CommandArguments()

    chatTriggerRemoved := false
    chatTriggers, exists := config.ChatTriggers[message.Chat.ID]
    if exists {
        newChatTriggers := []MyResponse{}
        for _, myResponse := range chatTriggers {
            if myResponse.SearchPhrase != removeSearchPhrase {
                newChatTriggers = append(newChatTriggers, myResponse)
            } else {
                chatTriggerRemoved = true
            }
        }
        config.ChatTriggers[message.Chat.ID] = newChatTriggers
    }

    err := configwriter.Put(config)
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

func handleRemoveGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config, configwriter ConfigWriter) error {
	log.Println("Handling /removeglobal command")

	// Check if the message comes from the allowed user
	if message.From.ID != int64(193117018) {
		msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
		_, _ = bot.Send(msg)
		return nil
	}

	removeSearchPhrase := message.CommandArguments()

	globalTriggerRemoved := false
	newMyResponses := []MyResponse{}
	for _, myResponse := range config.MyResponses {
		if myResponse.SearchPhrase != removeSearchPhrase {
			newMyResponses = append(newMyResponses, myResponse)
		} else {
			globalTriggerRemoved = true
		}
	}
	config.MyResponses = newMyResponses
	
	err := configwriter.Put(config)
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



func handleAddCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config, configwriter ConfigWriter) error {
	newSearchPhrase := message.CommandArguments()

	newMyResponse, err := createMyResponse(bot, message)
	if err != nil {
		log.Printf("Error creating MyResponse: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, "Can't add this trigger")
		bot.Send(msg)
		return err
	}
	newMyResponse.SearchPhrase = newSearchPhrase

	updateConfig(config, message, newMyResponse)

	err = configwriter.Put(config)
	if err != nil {
		log.Printf("Error saving config: %v", err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)

	return nil
}

func handleAddGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config, configwriter ConfigWriter) error {
	newSearchPhrase := message.CommandArguments()

	newMyResponse, err := createMyResponse(bot, message)
	if err != nil {
		log.Printf("Error creating MyResponse: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, "Can't add this trigger")
		bot.Send(msg)
		return err
	}
	newMyResponse.SearchPhrase = newSearchPhrase

	updateConfig(config, message, newMyResponse)

	err = configwriter.Put(config)
	if err != nil {
		log.Printf("Error saving config: %v", err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)

	return nil
}


func createMyResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message) (MyResponse, error) {
	var myResponse MyResponse

	if message.ReplyToMessage.Caption != "" {
		myResponse.Response = message.ReplyToMessage.Caption
	} else {
		myResponse.Response = message.ReplyToMessage.Text
	}

	if len(message.ReplyToMessage.Photo) > 0 { // photo proccess
		photoFileID := message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
		myResponse.FileType = FilePhoto
		myResponse.FileID = photoFileID
	} else if message.ReplyToMessage.Animation != nil { // gif proccess
		gifFileID := message.ReplyToMessage.Animation.FileID
		myResponse.FileType = FileGIF
		myResponse.FileID = gifFileID
	} else if message.ReplyToMessage.Voice != nil { // voice proccess
		voiceFileID := message.ReplyToMessage.Voice.FileID
		myResponse.FileType = FileVoice
		myResponse.FileID = voiceFileID
	} else if message.ReplyToMessage.Sticker != nil { // Sticker proccess
    	stickerFileID := message.ReplyToMessage.Sticker.FileID
    	myResponse.FileType = FileSticker
    	myResponse.FileID = stickerFileID
  	} else if message.ReplyToMessage.Video != nil { // Video proccess
    	videoFileID := message.ReplyToMessage.Video.FileID
    	myResponse.FileType = FileVideo
    	myResponse.FileID = videoFileID
  	} else if message.ReplyToMessage.Document != nil { // Document proccess
    	documentFileID := message.ReplyToMessage.Document.FileID
    	myResponse.FileType = FileDocument
    	myResponse.FileID = documentFileID
  	} else if message.ReplyToMessage.Audio != nil { // Audio proccess
    	audioFileID := message.ReplyToMessage.Audio.FileID
    	myResponse.FileType = FileAudio
    	myResponse.FileID = audioFileID
  	} else if message.ReplyToMessage.VideoNote != nil { // Document proccess
    	videonoteFileID := message.ReplyToMessage.VideoNote.FileID
    	myResponse.FileType = FileVideoNote
    	myResponse.FileID = videonoteFileID
  	} else if !allowedMessageType(message) {
		return myResponse, fmt.Errorf("Unsupported message type: %v", message)
	} else {
		return myResponse, nil
	}

	return myResponse, nil
}



func updateConfig(config *Config, message *tgbotapi.Message, myResponse MyResponse) {
	if message.Command() == "add" {
		if config.ChatTriggers == nil {
			config.ChatTriggers = make(map[int64][]MyResponse)
		}
		config.ChatTriggers[message.Chat.ID] = append(config.ChatTriggers[message.Chat.ID], myResponse)
	} else if message.Command() == "addglobal" {
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
	if myResponse.FileType == FilePhoto {
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		photoMsg.ReplyToMessageID = message.MessageID
		photoMsg.Caption = myResponse.Response
		return photoMsg, nil
	} else if myResponse.FileType == FileGIF {
		gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		gifMsg.ReplyToMessageID = message.MessageID
		gifMsg.Caption = myResponse.Response
		return gifMsg, nil
	} else if myResponse.FileType == FileVoice {
		voiceMsg := tgbotapi.NewVoice(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		voiceMsg.ReplyToMessageID = message.MessageID
		return voiceMsg, nil
	} else if myResponse.FileType == FileSticker {
    	stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
    	stickerMsg.ReplyToMessageID = message.MessageID
    	return stickerMsg, nil
    } else if myResponse.FileType == FileVideo {
    	videoMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
    	videoMsg.ReplyToMessageID = message.MessageID
		videoMsg.Caption = myResponse.Response
    	return videoMsg, nil
    } else if myResponse.FileType == FileDocument {
    	documentMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
    	documentMsg.ReplyToMessageID = message.MessageID
		documentMsg.Caption = myResponse.Response
    	return documentMsg, nil
    } else if myResponse.FileType == FileVideoNote {
    	videonoteMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
    	videonoteMsg.ReplyToMessageID = message.MessageID
    	return videonoteMsg, nil
    } else if myResponse.FileType == FileAudio {
    	audioMsg := tgbotapi.NewAudio(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
    	audioMsg.ReplyToMessageID = message.MessageID
		audioMsg.Caption = myResponse.Response
    	return audioMsg, nil
    } else {
		textMsg := tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
		textMsg.ReplyToMessageID = message.MessageID
		return textMsg, nil
	}
}

func handleChatIDCommand (bot *tgbotapi.BotAPI, message *tgbotapi.Message){
	chatid := "This chat ID is: " + strconv.FormatInt(message.Chat.ID, 10)
	msg := tgbotapi.NewMessage(message.Chat.ID, chatid)
	bot.Send(msg)
}


type commandHandlerFunc func(*tgbotapi.BotAPI, *tgbotapi.Message, *Config, ConfigWriter) error

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config, configwriter ConfigWriter, db *sql.DB) error {
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
			return handler(bot, message, config, configwriter)
		}
	}
	
	if command == "chatid" {
		handleChatIDCommand(bot, message)
	}

	if command == "generateqr" {
		handleGenerateQR(bot, message)
	}

	if command == "generatebar" {
		handleGenerateBarcode(bot, message)
	}

	if command == "addlocation" {
		timeAdd(bot, message, db)
	}

	if command == "removelocation" {
		timeRemove(bot, message, db)
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


func handleTriggersCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, config *Config, configwriter ConfigWriter) error {
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

func NewFileWriter(config *Config, configLocation string) (*FileWriter, chan *Config) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Panicf("Error creating file watcher: %v", err)
	}

	// Add the configuration file to the watcher
	err = watcher.Add(configlocation)
	if err != nil {
		log.Panicf("Error adding file to watcher: %v", err)
	}
	var FileWriter = &FileWriter{FileName: configlocation}

	// Create a config update channel
	configUpdate := make(chan *Config)

	// Goroutine to handle configuration file changes
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("Config file changed, reloading...")
					newConfig, err := readConfig(configlocation)
					if err != nil {
						log.Printf("Error reading config: %v", err)
					} else {
						configUpdate <- newConfig
					}
				}
			case err := <-watcher.Errors:
				log.Println("Error in file watcher:", err)
			}
		}
	}()

	return FileWriter, configUpdate
}

func handleGenerateQR(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	code := message.CommandArguments()
	chatID := message.Chat.ID
	filePath := fmt.Sprintf("./temp/%s.jpg", code)
    err := qrcode.WriteFile(strings.ToUpper(code), qrcode.Medium, 256, filePath)
    if err != nil {
        msg := tgbotapi.NewMessage(chatID, "Failed to generate QR code.")
		msg.ReplyToMessageID = message.MessageID
        bot.Send(msg)
        return
    }

    // Send the QR code
    msg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
    bot.Send(msg)
	err = os.Remove(filePath)
    if err != nil {
        // Log the error and return it
        log.Printf("Failed to delete file: %s, error: %v\n", filePath, err)
    }
}

func handleGenerateBarcode(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
    code := message.CommandArguments()
    chatID := message.Chat.ID
    filePath := fmt.Sprintf("./temp/%s.png", code)

    // Generate barcode
    bar, err := code128.Encode(code)
    if err != nil {
        msg := tgbotapi.NewMessage(chatID, "Failed to generate barcode.")
        msg.ReplyToMessageID = message.MessageID
        bot.Send(msg)
        return
    }

    // Scale the barcode to 300x100
    scaledBarcode, err := barcode.Scale(bar, 300, 100)
    if err != nil {
        msg := tgbotapi.NewMessage(chatID, "Failed to scale barcode.")
        msg.ReplyToMessageID = message.MessageID
        bot.Send(msg)
        return
    }

    // Create the file
    file, err := os.Create(filePath)
    if err != nil {
        msg := tgbotapi.NewMessage(chatID, "Failed to create file.")
        msg.ReplyToMessageID = message.MessageID
        bot.Send(msg)
        return
    }
    defer file.Close()

    // Encode the barcode as PNG
    err = png.Encode(file, scaledBarcode)
    if err != nil {
        msg := tgbotapi.NewMessage(chatID, "Failed to encode barcode.")
        msg.ReplyToMessageID = message.MessageID
        bot.Send(msg)
        return
    }

    // Send the barcode
    msg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
    bot.Send(msg)

    // Delete the file
    err = os.Remove(filePath)
    if err != nil {
        log.Printf("Failed to delete file: %s, error: %v\n", filePath, err)
    }
}