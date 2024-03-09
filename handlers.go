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

    return strings.Contains(normalizedMessage, normalizedTarget)
}


func handleRemoveCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
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
    newSearchPhrase := message.CommandArguments()

    newMyResponse, err := createMyResponse(bot, message)
    if err != nil {
        log.Printf("Error creating MyResponse: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Can't add this trigger")
        bot.Send(msg)
        return err
    }
    newMyResponse.SearchPhrase = newSearchPhrase

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

    newMyResponse, err := createMyResponse(bot, message)
    if err != nil {
        log.Printf("Error creating MyResponse: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Can't add this trigger")
        bot.Send(msg)
        return err
    }
    newMyResponse.SearchPhrase = newSearchPhrase

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

	if command == "alias" {
		addOrUpdateAlias(bot, message, db)
	}

	if command == "resetmessage" {
		resetMessage(bot, message, db)
	}

	if command == "samplesize" {
		handleSampleSize(bot, message)
	}

	currentTime,_ := getCurrentTimeForLocation("America/Los Angeles")
	currentTimeMoscow,_ := getCurrentTimeForLocation("Europe/Moscow")

	if (message.Chat.ID == -1001245934322 || message.Chat.ID == -1001390115843) && messageContains(receivedMessage, "@Porky8888") && isTimeBetween (currentTime, 2, 7) {
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID("AgACAgQAAx0Cc2pGjQACAUBlssL7rSKP4mmzMMYeORKjAS3LOAACHMIxGzznmFF5Spk5RRTfbwEAAwIAA3gAAzQE"))
		photoMsg.ReplyToMessageID = message.MessageID
		if isTimeBetween (currentTime, 2, 4) {
			photoMsg.Caption = fmt.Sprintf("Машталер в %v ночи", currentTime.Hour())
		} else {
			photoMsg.Caption = fmt.Sprintf("Машталер в %v утра", currentTime.Hour())
		}
		bot.Send(photoMsg)
	}

	if message.Chat.ID == -1001970411651 && messageContains(receivedMessage, "@vincenitycarter") && isTimeBetween19And8 (currentTimeMoscow) {
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID("AgACAgQAAx0Cc2pGjQACAX9ltZ3416cTOKI_-1Jp1wXzAVCLygACG74xGwkasVEOQZYuKQ4abQEAAwIAA3kAAzQE"))
		photoMsg.ReplyToMessageID = message.MessageID
		photoMsg.Caption = fmt.Sprintf("Сегодня, в %v, Яков Андреев был найден спящим в своей квартире. Приносим соболезнования всем его тиммейтам", currentTimeMoscow.Format("15:04"))
		bot.Send(photoMsg)
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
	   err := rows.Scan(
		   &trigger.ID,
		   &trigger.SearchPhrase,
		   &trigger.Response,
		   &trigger.FileType,
		   &trigger.FileID,
		   &trigger.FileName,
	   )
	   if err != nil {
		   log.Printf("Error scanning chat-specific trigger: %v", err)
		   continue
	   }
	   chatSpecificTriggers = append(chatSpecificTriggers, &trigger)
   }

   chatSpecificTriggerFound := false
   for _, trigger := range chatSpecificTriggers {
    if messageContains(receivedMessage, trigger.SearchPhrase) {
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
		   err := rows.Scan(
			   &trigger.ID,
			   &trigger.SearchPhrase,
			   &trigger.Response,
			   &trigger.FileType,
			   &trigger.FileID,
			   &trigger.FileName,
		   )
		   if err != nil {
			   log.Printf("Error scanning global trigger: %v", err)
			   continue
		   }
		   globalTriggers = append(globalTriggers, &trigger)
	   }

	   for _, trigger := range globalTriggers {
		if messageContains(receivedMessage, trigger.SearchPhrase) {
			err := processResponse(bot, message, *trigger)
			if err != nil {
				return err
			}
			break
		}
	}
   }

   return nil
}


func handleTriggersCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    log.Println("Handling /triggers command")

    // Retrieve chat-specific triggers from the database
    rows, err := db.Query(`
        SELECT search_phrase
        FROM triggers
        WHERE chat_id = ? AND is_global = ?
    `, message.Chat.ID, false)
    if err != nil {
        log.Printf("Error retrieving chat-specific triggers: %v", err)
        return err
    }
    defer rows.Close()

    chatSpecificTriggers := []MyResponse{}
    for rows.Next() {
        var trigger MyResponse
        err := rows.Scan(
            &trigger.ID,
            &trigger.SearchPhrase,
            &sql.NullString{},
            &sql.NullString{},
            &sql.NullString{},
            &sql.NullString{},
        )
        if err != nil {
            log.Printf("Error scanning chat-specific trigger: %v", err)
            continue
        }
        chatSpecificTriggers = append(chatSpecificTriggers, trigger)
    }

    // Retrieve global triggers from the database
    rows, err = db.Query(`
        SELECT search_phrase
        FROM triggers
        WHERE is_global = ?
    `, true)
    if err != nil {
        log.Printf("Error retrieving global triggers: %v", err)
        return err
    }
    defer rows.Close()

    globalTriggers := []MyResponse{}
    for rows.Next() {
        var trigger MyResponse
        err := rows.Scan(
            &trigger.ID,
            &trigger.SearchPhrase,
            &sql.NullString{},
            &sql.NullString{},
            &sql.NullString{},
            &sql.NullString{},
        )
        if err != nil {
            log.Printf("Error scanning global trigger: %v", err)
            continue
        }
        globalTriggers = append(globalTriggers, trigger)
    }

    // Extract search phrases from chat-specific triggers
    localTriggers := make([]string, len(chatSpecificTriggers))
    for i, trigger := range chatSpecificTriggers {
        localTriggers[i] = trigger.SearchPhrase
    }

    // Extract search phrases from global triggers
    globalTriggersStr := make([]string, len(globalTriggers))
    for i, trigger := range globalTriggers {
        globalTriggersStr[i] = trigger.SearchPhrase
    }

    localTriggersStr := strings.Join(localTriggers, ", ")
    globalTriggersJoined := strings.Join(globalTriggersStr, ", ")

    response := "Local Triggers:\n" + localTriggersStr + "\n\nGlobal Triggers:\n" + globalTriggersJoined

    msg := tgbotapi.NewMessage(message.Chat.ID, response)
    _, _ = bot.Send(msg)

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
