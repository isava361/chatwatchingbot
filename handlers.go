package main

import (
tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
"strings"
"os"
"log"
"fmt"
"strconv"
"github.com/skip2/go-qrcode"
"github.com/boombuler/barcode"
"github.com/boombuler/barcode/code128"
"image/png"
"database/sql"
"regexp"
"math/rand"
"math"
"time"
"errors"
"sort"
"golang.org/x/text/unicode/norm"
)

// SampleSize represents the sample sizes for different risk categories.
type SampleSize struct {
	Low, Medium, High int
}

type CascadeTrigger struct {
    ID           int
    SearchPhrase string
    Responses    []string
}

// Add this function to handle the /addc command

func handleAddCascadeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    if message.ReplyToMessage == nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Please reply to a message with the trigger phrase.")
        _, _ = bot.Send(msg)
        return nil
    }

    if message.ReplyToMessage.Text == ""{
        msg := tgbotapi.NewMessage(message.Chat.ID, "Please reply to a text message. Media is not supported with cascade triggers.")
        _, _ = bot.Send(msg)
        return nil
    }

    triggerPhrase := message.ReplyToMessage.Text
    newResponse := message.CommandArguments()

    if newResponse == "" {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide a response after the command.")
        _, _ = bot.Send(msg)
        return nil
    }

    // Insert new cascade trigger
    _, err := db.Exec(`
        INSERT INTO cascade_triggers (chat_id, search_phrase, responses)
        VALUES (?, ?, ?)
    `, message.Chat.ID, newResponse, triggerPhrase)

    if err != nil {
        log.Printf("Error inserting cascade trigger: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Failed to add cascade trigger.")
        _, _ = bot.Send(msg)
        return err
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, "Cascade trigger added successfully!")
    _, _ = bot.Send(msg)
    return nil
}

func handleRemoveCascadeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    // Check if the message is a reply
    if message.ReplyToMessage == nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Please reply to a bot's message with /removec to remove the cascade trigger.")
        _, _ = bot.Send(msg)
        return nil
    }

    // Check if the replied message is from the bot
    if message.ReplyToMessage.From.UserName != bot.Self.UserName {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Please reply to a message from the bot to remove the cascade trigger.")
        _, _ = bot.Send(msg)
        return nil
    }

    // Get the trigger phrase from the replied message
    triggerPhrase := message.ReplyToMessage.Text

    // Remove the cascade trigger from the database
    result, err := db.Exec(`
        DELETE FROM cascade_triggers
        WHERE chat_id = ? AND responses = ?
    `, message.Chat.ID, triggerPhrase)
    
    if err != nil {
        log.Printf("Error removing cascade trigger: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Failed to remove cascade trigger. Please try again.")
        _, _ = bot.Send(msg)
        return err
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Printf("Error getting rows affected: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "An error occurred while removing the cascade trigger.")
        _, _ = bot.Send(msg)
        return err
    }

    if rowsAffected > 0 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Cascade trigger response removed successfully!")
        _, _ = bot.Send(msg)
    } else {
        msg := tgbotapi.NewMessage(message.Chat.ID, "No cascade trigger found with this response.")
        _, _ = bot.Send(msg)
    }

    return nil
}


func allowedMessageType(message *tgbotapi.Message) bool {
	if (message.ReplyToMessage.Game != nil){
		return false
	}
	return true
}

func messageContains(messageText, targetString string) bool {
    // Normalize the message text and target string to NFC form
    normalizedMessage := norm.NFC.String(messageText)
    normalizedTarget := norm.NFC.String(targetString)

    // Convert both strings to lowercase for case-insensitive comparison
    lowercaseMessage := strings.ToLower(normalizedMessage)
    lowercaseTarget := strings.ToLower(normalizedTarget)

    return strings.Contains(lowercaseMessage, lowercaseTarget)
}

func handleRemoveCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    if message.From.ID == 89886125 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Дима саси жопу")
        bot.Send(msg)
        return nil
    }
    removeSearchPhrase := message.CommandArguments()

    // Delete the trigger from the database
    result, err := db.Exec(`
        DELETE FROM triggers
        WHERE chat_id = ? AND search_phrase = ? AND is_global = ?
    `, message.Chat.ID, removeSearchPhrase, false)
    if err != nil {
        log.Printf("Error deleting trigger: %v", err)
        return err
    }

    rowsAffected, _ := result.RowsAffected()
    if rowsAffected > 0 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Local response removed!")
        _, _ = bot.Send(msg)
    } else {
        msg := tgbotapi.NewMessage(message.Chat.ID, "No local response found with that search phrase.")
        _, _ = bot.Send(msg)
    }

    return nil
}

func handleRemoveGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    // Check if the message comes from the allowed user
    if message.From.ID != int64(193117018) {
        msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
        _, _ = bot.Send(msg)
        return nil
    }

    removeSearchPhrase := message.CommandArguments()

    // Delete the global trigger from the database
    result, err := db.Exec(`
        DELETE FROM triggers
        WHERE search_phrase = ? AND is_global = ?
    `, removeSearchPhrase, true)
    if err != nil {
        log.Printf("Error deleting global trigger: %v", err)
        return err
    }

    rowsAffected, _ := result.RowsAffected()
    if rowsAffected > 0 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Global response removed!")
        _, _ = bot.Send(msg)
    } else {
        msg := tgbotapi.NewMessage(message.Chat.ID, "No global response found with that search phrase.")
        _, _ = bot.Send(msg)
    }

    return nil
}


func handleAddCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {

    if message.From.ID == 89886125 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Дима саси жопу")
        bot.Send(msg)
        return nil
    }

    newSearchPhrase := message.CommandArguments()

    // Check if the trigger already exists for the specific chat
    var count int
    err := db.QueryRow(`
        SELECT COUNT(*) FROM triggers
        WHERE chat_id = ? AND search_phrase = ? AND is_global = ?
    `, message.Chat.ID, newSearchPhrase, false).Scan(&count)
    if err != nil {
        log.Printf("Error checking trigger existence: %v", err)
        return err
    }

    newMyResponse, err := createMyResponse(bot, message)
    if err != nil {
        log.Printf("Error creating MyResponse: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Can't add this trigger")
        bot.Send(msg)
        return err
    }
    newMyResponse.SearchPhrase = newSearchPhrase


    if count > 0 {
        // Update the trigger in the database
        _, err = db.Exec(`
            UPDATE triggers 
            SET response = ?, file_type = ?, file_id = ?, file_name = ? 
            WHERE chat_id = ? AND search_phrase = ? AND is_global = ?
        `, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, message.Chat.ID, newMyResponse.SearchPhrase, false)
        if err != nil {
            log.Printf("Error updating trigger: %v", err)
            return err
        }
    
        msg := tgbotapi.NewMessage(message.Chat.ID, "Response updated!")
        _, _ = bot.Send(msg)
    
        return nil
    }

    // Insert the trigger into the database
    _, err = db.Exec(`
        INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, is_global)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, message.Chat.ID, newMyResponse.SearchPhrase, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, false)
    if err != nil {
        log.Printf("Error inserting trigger: %v", err)
        return err
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
    _, _ = bot.Send(msg)

    return nil
}

func handleAddGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    // Check if the message comes from the allowed user
    if message.From.ID != int64(193117018) {
        msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
        _, _ = bot.Send(msg)
        return nil
    }

    newSearchPhrase := message.CommandArguments()

    // Check if the global trigger already exists
    var count int
    err := db.QueryRow(`
        SELECT COUNT(*) FROM triggers
        WHERE search_phrase = ? AND is_global = ?
    `, newSearchPhrase, true).Scan(&count)
    if err != nil {
        log.Printf("Error checking global trigger existence: %v", err)
        return err
    }

    newMyResponse, err := createMyResponse(bot, message)
    if err != nil {
        log.Printf("Error creating MyResponse: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Can't add this trigger")
        bot.Send(msg)
        return err
    }
    newMyResponse.SearchPhrase = newSearchPhrase

    if count > 0 {
        // Update the global trigger into the database
        _, err = db.Exec(`
            UPDATE triggers 
            SET response = ?, file_type = ?, file_id = ?, file_name = ? 
            WHERE chat_id = ? AND search_phrase = ? AND is_global = ?
        `, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, 0, newMyResponse.SearchPhrase, true)
        if err != nil {
            log.Printf("Error updating global trigger: %v", err)
            return err
        }
    
        msg := tgbotapi.NewMessage(message.Chat.ID, "Response updated!")
        _, _ = bot.Send(msg)
    
        return nil
    }

    // Insert the global trigger into the database
    _, err = db.Exec(`
        INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, is_global)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, 0, newMyResponse.SearchPhrase, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, true)
    if err != nil {
        log.Printf("Error inserting global trigger: %v", err)
        return err
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, "New global response added!")
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


type commandHandlerFunc func(*tgbotapi.BotAPI, *tgbotapi.Message, *sql.DB) error


func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	receivedMessage := message.Text

	if message.NewChatMembers != nil {
		if err := handleNewMember(bot, message); err != nil {
			log.Printf("Error handling new member: %v", err)
		}
	}

	if err := handleTerpetMessage(bot, message, db); err != nil {
		return err
	}

	if message.Command() == "topterpil" {
		if err := handleTopTerpilCommand(bot, message, db); err != nil {
			return err
		}
	}

	if message.Command() == "getlink" {
		handleGetLinkCommand(bot, message, db)
		return nil
	}

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
			return handler(bot, message, db)
		}
	}

	if command == "chatid" {
		handleChatIDCommand(bot, message)
		return nil
	}

	if command == "generateqr" {
		handleGenerateQR(bot, message)
		return nil
	}

	if command == "generatebar" {
		handleGenerateBarcode(bot, message)
		return nil
	}

	if command == "addlocation" {
		timeAdd(bot, message, db)
		return nil
	}

	if command == "removelocation" {
		timeRemove(bot, message, db)
		return nil
	}

	if command == "alias" {
		addOrUpdateAlias(bot, message, db)
		return nil
	}

	if command == "resetmessage" {
		resetMessage(bot, message, db)
		return nil
	}

	if command == "samplesize" {
		handleSampleSize(bot, message)
		return nil
	}

    if message.Command() == "roll" {
        return handleRoll(bot, message)
    }

    if message.Command() == "roll20" {
        return handleRoll20(bot, message)
    }

    if message.Command() == "roll12" {
        return handleRoll12(bot, message)
    }

    if message.Command() == "roll10" {
        return handleRoll10(bot, message)
    }

    if message.Command() == "roll8" {
        return handleRoll8(bot, message)
    }

    if message.Command() == "roll6" {
        return handleRoll6(bot, message)
    }

    if message.Command() == "roll4" {
        return handleRoll4(bot, message)
    }

    if message.Command() == "addc" {
        return handleAddCascadeCommand(bot, message, db)
    }

    if message.Command() == "removec" {
        return handleRemoveCascadeCommand(bot, message, db)
    }

	currentTime, _ := getCurrentTimeForLocation("America/Los Angeles")
	currentTimeMoscow, _ := getCurrentTimeForLocation("Europe/Moscow")
    currentTimeNewYork, _ := getCurrentTimeForLocation("America/New York")

	if (message.Chat.ID == -1001245934322 || message.Chat.ID == -1001390115843) && messageMatches(receivedMessage, "@Porky8888") && isTimeBetween(currentTime, 2, 7) {
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID("AgACAgQAAx0Cc2pGjQACAUBlssL7rSKP4mmzMMYeORKjAS3LOAACHMIxGzznmFF5Spk5RRTfbwEAAwIAA3gAAzQE"))
		photoMsg.ReplyToMessageID = message.MessageID
		if isTimeBetween(currentTime, 2, 4) {
			photoMsg.Caption = fmt.Sprintf("Машталер в %v ночи", currentTime.Hour())
		} else {
			photoMsg.Caption = fmt.Sprintf("Машталер в %v утра", currentTime.Hour())
		}
		bot.Send(photoMsg)
		return nil
	}

	if message.Chat.ID == -1001970411651 && messageMatches(receivedMessage, "@vincenitycarter") && isTimeBetween19And8(currentTimeMoscow) {
		rand.Seed(time.Now().UnixNano())

		fileID := "AgACAgQAAx0Cc2pGjQACAX9ltZ3416cTOKI_-1Jp1wXzAVCLygACG74xGwkasVEOQZYuKQ4abQEAAwIAA3kAAzQE"
		if rand.Float32() < 0.5 {
			fileID = "AgACAgIAAx0Cc2pGjQACAnVmeHhbXkkqgeg_DNEW1dChwB3BYQACuNoxG2g9yUsZaxbgiGFD_wEAAwIAA3kAAzUE"
		}

		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(fileID))
		photoMsg.ReplyToMessageID = message.MessageID
		photoMsg.Caption = fmt.Sprintf("Сегодня, в %v, Яков Андреев был найден спящим в своей квартире. Приносим соболезнования всем его тиммейтам", currentTimeMoscow.Format("15:04"))
		bot.Send(photoMsg)
		return nil
	}

    if message.Chat.ID == -1002245157577 && messageMatches(receivedMessage, "@KelThuzad") && isTimeBetween(currentTimeNewYork, 2,7) {
		rand.Seed(time.Now().UnixNano())

		fileID := "AgACAgQAAx0Cc2pGjQACArNm0PVZDzYsYwqBhiOBkCD4rCu8cQAC-78xGxt-iFJZyKNkTiV9hQEAAwIAA3gAAzUE"
		if rand.Float32() < 0.5 {
			fileID = "AgACAgQAAx0Cc2pGjQACArNm0PVZDzYsYwqBhiOBkCD4rCu8cQAC-78xGxt-iFJZyKNkTiV9hQEAAwIAA3gAAzUE"
		}

		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(fileID))
		photoMsg.ReplyToMessageID = message.MessageID
        if isTimeBetween(currentTimeNewYork, 2, 4) {
			photoMsg.Caption = fmt.Sprintf("Кел в %v ночи", currentTimeNewYork.Hour())
		} else {
			photoMsg.Caption = fmt.Sprintf("Кел в %v утра", currentTimeNewYork.Hour())
		}
        bot.Send(photoMsg)
		return nil
	}

	// Retrieve chat-specific triggers from the database
	rows, err := db.Query(`
		SELECT id, search_phrase, response, file_type, file_id, file_name
		FROM triggers
		WHERE chat_id = ? AND is_global = ?
	`, message.Chat.ID, false)
	if err != nil {
		log.Printf("Error retrieving chat-specific triggers: %v", err)
		return err
	}
	defer rows.Close()

	chatSpecificTriggers := []*MyResponse{}
	for rows.Next() {
		var trigger MyResponse
		var response, fileType, fileID, fileName sql.NullString
		err := rows.Scan(
			&trigger.ID,
			&trigger.SearchPhrase,
			&response,
			&fileType,
			&fileID,
			&fileName,
		)
		if err != nil {
			log.Printf("Error scanning chat-specific trigger: %v", err)
			continue
		}
		trigger.Response = response.String
		trigger.FileType = FileType(fileType.String)
		trigger.FileID = fileID.String
		trigger.FileName = fileName.String
		chatSpecificTriggers = append(chatSpecificTriggers, &trigger)
	}

	chatSpecificTriggerFound := false
	for _, trigger := range chatSpecificTriggers {
		if messageMatches(receivedMessage, trigger.SearchPhrase) {
			err := processResponse(bot, message, *trigger)
			if err != nil {
				return err
			}
			chatSpecificTriggerFound = true
			break
		}
	}

	if !chatSpecificTriggerFound {
		// Retrieve global triggers from the database
		rows, err := db.Query(`
			SELECT id, search_phrase, response, file_type, file_id, file_name
			FROM triggers
			WHERE is_global = ?
		`, true)
		if err != nil {
			log.Printf("Error retrieving global triggers: %v", err)
			return err
		}
		defer rows.Close()

		globalTriggers := []*MyResponse{}
		for rows.Next() {
			var trigger MyResponse
			var response, fileType, fileID, fileName sql.NullString
			err := rows.Scan(
				&trigger.ID,
				&trigger.SearchPhrase,
				&response,
				&fileType,
				&fileID,
				&fileName,
			)
			if err != nil {
				log.Printf("Error scanning global trigger: %v", err)
				continue
			}
			trigger.Response = response.String
			trigger.FileType = FileType(fileType.String)
			trigger.FileID = fileID.String
			trigger.FileName = fileName.String
			globalTriggers = append(globalTriggers, &trigger)
		}

		for _, trigger := range globalTriggers {
			if messageMatches(receivedMessage, trigger.SearchPhrase) {
				err := processResponse(bot, message, *trigger)
				if err != nil {
					return err
				}
				break
			}
		}
	}

    // Process cascade triggers
    // Process cascade triggers
    rows, err = db.Query(`
        SELECT responses
        FROM cascade_triggers
        WHERE chat_id = ? AND search_phrase = ?
    `, message.Chat.ID, message.Text)

    if err != nil {
        log.Printf("Error querying cascade triggers: %v", err)
        return err
    }
    defer rows.Close()

    var responses []string
    for rows.Next() {
        var response string
        if err := rows.Scan(&response); err != nil {
            log.Printf("Error scanning cascade trigger response: %v", err)
            continue
        }
        responses = append(responses, response)
    }

    if len(responses) > 0 {
        for _, response := range responses {
            msg := tgbotapi.NewMessage(message.Chat.ID, response)
            _, _ = bot.Send(msg)
        }
        return nil // Return after processing cascade triggers
    }

	if message.From.ID == 578801 {
		rand.Seed(time.Now().UnixNano())

		if message.Photo == nil && 
		   message.Animation == nil && 
		   message.Sticker == nil && 
		   message.Voice == nil && 
		   message.Video == nil && 
		   message.Document == nil && 
		   message.VideoNote == nil && 
		   message.Audio == nil {
			if rand.Float32() < 0.01 {
				vasyaMsg := tgbotapi.NewMessage(message.Chat.ID, "хуйню написал")
				vasyaMsg.ReplyToMessageID = message.MessageID
				bot.Send(vasyaMsg)
			}
		} else {
			if rand.Float32() < 0.01 {
				vasyaMsg := tgbotapi.NewMessage(message.Chat.ID, "хуйню прислал")
				vasyaMsg.ReplyToMessageID = message.MessageID
				bot.Send(vasyaMsg)
			}
		}
	}

	return nil
}

func messageMatches(messageText, targetString string) bool {
	// Normalize both strings to NFC form and convert to lowercase
	normalizedMessage := strings.ToLower(norm.NFC.String(messageText))
	normalizedTarget := strings.ToLower(norm.NFC.String(targetString))

	return normalizedMessage == normalizedTarget
}


func handleTriggersCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    log.Println("Handling /triggers command")

    // Retrieve chat-specific trigger phrases from the database
    rows, err := db.Query(`
        SELECT DISTINCT search_phrase
        FROM triggers
        WHERE chat_id = ? AND is_global = ?
    `, message.Chat.ID, false)
    if err != nil {
        log.Printf("Error retrieving chat-specific trigger phrases: %v", err)
        return err
    }
    defer rows.Close()

    chatSpecificTriggers := []string{}
    for rows.Next() {
        var searchPhrase string
        err := rows.Scan(&searchPhrase)
        if err != nil {
            log.Printf("Error scanning chat-specific trigger phrase: %v", err)
            continue
        }
        chatSpecificTriggers = append(chatSpecificTriggers, searchPhrase)
    }

    // Retrieve global trigger phrases from the database
    rows, err = db.Query(`
        SELECT DISTINCT search_phrase
        FROM triggers
        WHERE is_global = ?
    `, true)
    if err != nil {
        log.Printf("Error retrieving global trigger phrases: %v", err)
        return err
    }
    defer rows.Close()

    globalTriggers := []string{}
    for rows.Next() {
        var searchPhrase string
        err := rows.Scan(&searchPhrase)
        if err != nil {
            log.Printf("Error scanning global trigger phrase: %v", err)
            continue
        }
        globalTriggers = append(globalTriggers, searchPhrase)
    }

    // Retrieve cascade trigger phrases from the database
    rows, err = db.Query(`
        SELECT DISTINCT search_phrase
        FROM cascade_triggers
        WHERE chat_id = ?
    `, message.Chat.ID)
    if err != nil {
        log.Printf("Error retrieving cascade trigger phrases: %v", err)
        return err
    }
    defer rows.Close()

    cascadeTriggers := []string{}
    for rows.Next() {
        var searchPhrase string
        err := rows.Scan(&searchPhrase)
        if err != nil {
            log.Printf("Error scanning cascade trigger phrase: %v", err)
            continue
        }
        cascadeTriggers = append(cascadeTriggers, searchPhrase)
    }

    localTriggersStr := strings.Join(chatSpecificTriggers, ", ")
    globalTriggersStr := strings.Join(globalTriggers, ", ")
    cascadeTriggersStr := strings.Join(cascadeTriggers, ", ")

    response := fmt.Sprintf("Local Triggers:\n%s\n\nGlobal Triggers:\n%s\n\nCascade Triggers:\n%s",
        localTriggersStr, globalTriggersStr, cascadeTriggersStr)

    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    _, err = bot.Send(msg)
    if err != nil {
        log.Printf("Error sending triggers message: %v", err)
        return err
    }

    return nil
}

func handleGenerateQR(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	code := message.CommandArguments()

	// Sanitize the code to be used in file path
	sanitizedCode := sanitizeFileName(code)

	chatID := message.Chat.ID
	filePath := fmt.Sprintf("./temp/%s.jpg", sanitizedCode)
	err := qrcode.WriteFile(code, qrcode.Medium, 256, filePath)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to generate QR code.")
		msg.ReplyToMessageID = message.MessageID
		bot.Send(msg)
		log.Printf("Failed to create QR: %v\n", err)
		return
	}

	// Send the QR code
	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	bot.Send(msg)
	err = os.Remove(filePath)
	if err != nil {
		// Log the error
		log.Printf("Failed to delete file: %s, error: %v\n", filePath, err)
	}
}

func sanitizeFileName(fileName string) string {
	// Replace invalid characters with underscore
	re := regexp.MustCompile(`[^a-zA-Z0-9.-]`)
	return re.ReplaceAllString(fileName, "_")
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

func handleSampleSize(bot *tgbotapi.BotAPI, message *tgbotapi.Message){
	commandText := message.Text // Assuming message.Text contains the full message
	riskCategory, populationSize, err := parseCommandArguments(commandText)
	if err != nil {
		// handle error, for example, send a message back to the user indicating the correct format
		fmt.Println(err)
		return
	}

	sampleSize, err := GetSampleSize(populationSize, riskCategory)
	if err != nil {
		fmt.Println(err)
		return
	}

	randomSelection := GenerateRandomSelection(sampleSize, populationSize)

	// Create the initial part of the message
	messageText := fmt.Sprintf("For %s risk and population of %d, sample size is %d\n", riskCategory, populationSize, sampleSize)

	// Append the list of random numbers
	messageText += "Random numbers for random selection: \n"
	for _, num := range randomSelection {
		messageText += fmt.Sprintf("%d \n", num)
	}

	// Send the message
	msg := tgbotapi.NewMessage(message.Chat.ID, messageText)
	bot.Send(msg)
}

func parseCommandArguments(commandText string) (string, int, error) {
    parts := strings.Fields(commandText)

    // Expecting at least 3 parts: the command, risk, and population
    if len(parts) < 3 {
        return "", 0, errors.New("invalid command format")
    }

    risk := parts[1]
    populationStr := parts[2]

    // Convert population string to an integer
    population, err := strconv.Atoi(populationStr)
    if err != nil {
        return "", 0, fmt.Errorf("invalid population number: %s", populationStr)
    }

    return risk, population, nil
}

func GetSampleSize(population int, risk string) (int, error) {
    var sampleSizes = []struct {
        MinPopulation int
        MaxPopulation int
        Sizes         SampleSize
    }{
        {0, 1, SampleSize{1, 1, 1}},
        {2, 4, SampleSize{2, 2, 2}},
        {5, 12, SampleSize{2, 3, 5}},
        {13, 52, SampleSize{5, 10, 15}},
        {53, 250, SampleSize{20, 30, 40}},
        {251, int(^uint(0) >> 1), SampleSize{25, 45, 60}},
    }
// Directly return the size for populations above 250
if population > 250 {
	lastSize := sampleSizes[len(sampleSizes)-1].Sizes
	return getRiskSize(lastSize, risk), nil
}

    for i, size := range sampleSizes {
        if population <= size.MaxPopulation {
            if i == 0 || population == size.MaxPopulation {
                return getRiskSize(size.Sizes, risk), nil
            }

            prevSize := sampleSizes[i-1]
            y0 := float64(getRiskSize(prevSize.Sizes, risk))
            y1 := float64(getRiskSize(size.Sizes, risk))
            interpolatedValue := interpolate(float64(prevSize.MaxPopulation), y0, float64(population), float64(size.MaxPopulation), y1)
            return int(math.Ceil(interpolatedValue)), nil
        }
    }

    return 0, errors.New("population out of range")
}

func getRiskSize(sizes SampleSize, risk string) int {
    switch risk {
    case "low":
        return sizes.Low
    case "medium":
        return sizes.Medium
    case "high":
        return sizes.High
    default:
        return 0
    }
}

func interpolate(x0, y0, x, x1, y1 float64) float64 {
    if x1 == x0 {
        return y0
    }
    return y0 + (y1-y0)*(x-x0)/(x1-x0)
}


func GenerateRandomSelection(sampleSize, population int) []int {
    rand.Seed(time.Now().UnixNano())

    if sampleSize > population {
        // Cannot have more unique numbers than the population
        return nil
    }

    selection := make([]int, 0, sampleSize)
    generated := make(map[int]bool)

    for len(selection) < sampleSize {
        num := rand.Intn(population) + 1
        if !generated[num] {
            generated[num] = true
            selection = append(selection, num)
        }
    }

    // Sort the selection in ascending order
    sort.Ints(selection)

    return selection
}


func handleTerpetMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    if message.Chat.ID == -1001390115843 && (message.Text == "Терпеть" || message.Text == "/terpet") {
        userID := message.From.ID
        username := message.From.UserName
        firstName := message.From.FirstName

        // Insert or update the user's terpet count
        _, err := db.Exec(`
            INSERT INTO terpet_count (user_id, username, first_name, count)
            VALUES (?, ?, ?, 1)
            ON CONFLICT(user_id) DO UPDATE SET count = count + 1, username = ?, first_name = ?
        `, userID, username, firstName, username, firstName)
        if err != nil {
            log.Printf("Error updating terpet count: %v", err)
            return err
        }

        // Get the updated count for the user
        var count int
        err = db.QueryRow("SELECT count FROM terpet_count WHERE user_id = ?", userID).Scan(&count)
        if err != nil {
            log.Printf("Error retrieving terpet count: %v", err)
            return err
        }

        razForm := getRazForm(count)
        msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Вы терпели %d %s", count, razForm))
        msg.ReplyToMessageID = message.MessageID
        _, err = bot.Send(msg)
        if err != nil {
            log.Printf("Error sending terpet message: %v", err)
            return err
        }
    }
    return nil
}

func handleTopTerpilCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    rows, err := db.Query(`
        SELECT COALESCE(NULLIF(username, ''), first_name) AS name, count
        FROM terpet_count
        ORDER BY count DESC
        LIMIT 5
    `)
    if err != nil {
        log.Printf("Error retrieving top terpet counts: %v", err)
        return err
    }
    defer rows.Close()

    var topUsers []string
    for rows.Next() {
        var name string
        var count int
        err := rows.Scan(&name, &count)
        if err != nil {
            log.Printf("Error scanning top terpet count: %v", err)
            continue
        }
        topUsers = append(topUsers, fmt.Sprintf("%s: %d %s", name, count, getRazForm(count)))
    }

    if len(topUsers) == 0 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "No terpet data available.")
        _, err = bot.Send(msg)
        if err != nil {
            log.Printf("Error sending top terpil message: %v", err)
            return err
        }
    } else {
        response := "Топ-5 Терпил:\n" + strings.Join(topUsers, "\n")
        msg := tgbotapi.NewMessage(message.Chat.ID, response)
        _, err = bot.Send(msg)
        if err != nil {
            log.Printf("Error sending top terpil message: %v", err)
            return err
        }
    }

    return nil
}

func getRazForm(count int) string {
    if count%10 == 1 && count%100 != 11 {
        return "раз"
    } else if (count%10 >= 2 && count%10 <= 4 && (count%100 < 10 || count%100 >= 20)) || (count%10 == 0) {
        return "раза"
    } else {
        return "раз"
    }
}
func handleGetLinkCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	if message.From.ID == int64(193117018) {
		args := message.CommandArguments()
		if args == "" {
			msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide an ID.")
			_, _ = bot.Send(msg)
			return nil
		}

		link := "<a href='tg://user?id=" + args + "'>Link to User</a>"
		msg := tgbotapi.NewMessage(message.Chat.ID, link)
		msg.ParseMode = "HTML"
        msg.DisableWebPagePreview = true
		_, _ = bot.Send(msg)
	}
	return nil
}

func handleNewMember(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    if message.NewChatMembers != nil {
        // Sticker set name for the "privetcivpack_by_fStikBot" pack
        stickerSetName := "privetcivpack_by_fStikBot"
        
        // Get the sticker set
        config := tgbotapi.GetStickerSetConfig{
            Name: stickerSetName,
        }
        stickerSet, err := bot.GetStickerSet(config)
        if err != nil {
            log.Printf("Error getting sticker set: %v", err)
            return err
        }
        
        // Choose a random sticker from the set
        rand.Seed(time.Now().UnixNano())
        randomIndex := rand.Intn(len(stickerSet.Stickers))
        randomSticker := stickerSet.Stickers[randomIndex]
        
        // Create a new sticker message
        stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(randomSticker.FileID))
        
        // Send the sticker
        _, err = bot.Send(stickerMsg)
        if err != nil {
            log.Printf("Error sending sticker: %v", err)
            return err
        }
    }
    return nil
}

func handleRoll(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 100
    result := rand.Intn(100) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}

func handleRoll20(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 20
    result := rand.Intn(20) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}

func handleRoll12(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 12
    result := rand.Intn(12) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}

func handleRoll10(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 10
    result := rand.Intn(10) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}

func handleRoll8(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 8
    result := rand.Intn(8) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}

func handleRoll6(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 6
    result := rand.Intn(6) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}

func handleRoll4(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
    // Seed the random number generator
    rand.Seed(time.Now().UnixNano())

    // Generate a random number between 1 and 4
    result := rand.Intn(4) + 1

    // Create the response message
    response := fmt.Sprintf("🎲 You rolled: %d", result)
    
    // Send the message
    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    msg.ReplyToMessageID = message.MessageID
    
    _, err := bot.Send(msg)
    if err != nil {
        log.Printf("Error sending roll result: %v", err)
        return err
    }

    return nil
}