package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/skip2/go-qrcode"
	"golang.org/x/text/unicode/norm"
)

// lastKeywordTimestamps will hold the last time each keyword was triggered in each chat.
var (
	// For example, you could have a key like "chatID:keyword"
	lastKeywordTimestamps = make(map[string]time.Time)
	timestampsMu          sync.Mutex
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

// --- Seed the random generator once (Issue 2) ---
func init() {
	rand.Seed(time.Now().UnixNano())
}

// checkTriggerExistence converts the search phrase to lower case and returns
// two booleans indicating whether a local trigger and a cascade trigger exist.
func checkTriggerExistence(db *sql.DB, chatID int64, searchPhrase string) (bool, bool, error) {
	var normalCount, cascadeCount int
	searchPhrase = strings.ToLower(searchPhrase)

	err := db.QueryRow(`
        SELECT COUNT(*) FROM triggers 
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
		chatID, searchPhrase, false).Scan(&normalCount)
	if err != nil {
		return false, false, err
	}

	err = db.QueryRow(`
        SELECT COUNT(*) FROM cascade_triggers2 
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?)`,
		chatID, searchPhrase).Scan(&cascadeCount)
	if err != nil {
		return false, false, err
	}

	return normalCount > 0, cascadeCount > 0, nil
}

// handleAddCascadeCommand creates a cascade trigger using the replied message.
func handleAddCascadeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	// Ensure the command is a reply to a message
	if message.ReplyToMessage == nil {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Please reply to the message you want to use as a response when adding a cascade trigger.\nExample: /addc Hello")
		_, _ = bot.Send(msg)
		return nil
	}

	// Extract and normalize the trigger phrase
	triggerPhrase := strings.TrimSpace(message.CommandArguments())
	if triggerPhrase == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide a trigger phrase after the /addc command.\nExample: /addc Hello")
		_, _ = bot.Send(msg)
		return nil
	}
	triggerPhrase = strings.ToLower(triggerPhrase)

	// Check if a local trigger exists and prevent duplicate cascade triggers
	localExists, _, err := checkTriggerExistence(db, message.Chat.ID, triggerPhrase)
	if err != nil {
		return err
	}
	if localExists {
		msg := tgbotapi.NewMessage(message.Chat.ID, "A local trigger with this phrase already exists. Cannot create a cascade trigger.")
		_, _ = bot.Send(msg)
		return nil
	}

	// Check if a cascade trigger with the same phrase already exists
	var existingTriggerID int64
	err = db.QueryRow(
		`SELECT id FROM cascade_triggers2
         WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?)`,
		message.Chat.ID, triggerPhrase).Scan(&existingTriggerID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	var triggerID int64
	if err == sql.ErrNoRows {
		// Insert a new cascade trigger
		result, err := db.Exec(
			`INSERT INTO cascade_triggers2 (chat_id, search_phrase)
             VALUES (?, ?)`,
			message.Chat.ID, triggerPhrase)
		if err != nil {
			log.Printf("Error adding new cascade trigger: %v", err)
			msg := tgbotapi.NewMessage(message.Chat.ID, "Failed to add cascade trigger.")
			_, _ = bot.Send(msg)
			return err
		}
		triggerID, err = result.LastInsertId()
		if err != nil {
			log.Printf("Error getting LastInsertId: %v", err)
			return err
		}
	} else {
		// Use the existing cascade trigger ID
		triggerID = existingTriggerID
	}

	// Determine the response content (text or caption)
	var responseText string
	if message.ReplyToMessage.Text != "" {
		responseText = strings.TrimSpace(message.ReplyToMessage.Text)
	} else if message.ReplyToMessage.Caption != "" {
		responseText = strings.TrimSpace(message.ReplyToMessage.Caption)
	}

	// Get entities from the replied message
	var entities []tgbotapi.MessageEntity
	if len(message.ReplyToMessage.Entities) > 0 {
		entities = filterCustomEmojiEntities(message.ReplyToMessage.Entities)
	} else if len(message.ReplyToMessage.CaptionEntities) > 0 {
		entities = filterCustomEmojiEntities(message.ReplyToMessage.CaptionEntities)
	}

	// Prepare a single MyResponse struct.
	var myResponse MyResponse
	myResponse.SearchPhrase = triggerPhrase
	myResponse.Entities = entities

	// Determine which response to use, giving media priority if available.
	// Only one response entry will be inserted.
	if message.ReplyToMessage.Photo != nil && len(message.ReplyToMessage.Photo) > 0 {
		// Use the highest resolution photo (last in the array)
		photo := message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1]
		myResponse.FileType = FilePhoto
		myResponse.FileID = photo.FileID
		myResponse.FileName = ""
		// Use caption if available
		if responseText != "" {
			myResponse.Response = responseText
		}
	} else if message.ReplyToMessage.Animation != nil {
		myResponse.FileType = FileGIF
		myResponse.FileID = message.ReplyToMessage.Animation.FileID
		myResponse.FileName = ""
		if responseText != "" {
			myResponse.Response = responseText
		}
	} else if message.ReplyToMessage.Voice != nil {
		myResponse.FileType = FileVoice
		myResponse.FileID = message.ReplyToMessage.Voice.FileID
		myResponse.FileName = ""
	} else if message.ReplyToMessage.Sticker != nil {
		myResponse.FileType = FileSticker
		myResponse.FileID = message.ReplyToMessage.Sticker.FileID
		myResponse.FileName = ""
	} else if message.ReplyToMessage.Video != nil {
		myResponse.FileType = FileVideo
		myResponse.FileID = message.ReplyToMessage.Video.FileID
		myResponse.FileName = ""
		if responseText != "" {
			myResponse.Response = responseText
		}
	} else if message.ReplyToMessage.Document != nil {
		myResponse.FileType = FileDocument
		myResponse.FileID = message.ReplyToMessage.Document.FileID
		myResponse.FileName = message.ReplyToMessage.Document.FileName
	} else if message.ReplyToMessage.Audio != nil {
		myResponse.FileType = FileAudio
		myResponse.FileID = message.ReplyToMessage.Audio.FileID
		myResponse.FileName = message.ReplyToMessage.Audio.FileName
	} else if message.ReplyToMessage.VideoNote != nil {
		myResponse.FileType = FileVideoNote
		myResponse.FileID = message.ReplyToMessage.VideoNote.FileID
		myResponse.FileName = ""
	} else if responseText != "" {
		// Only text is present
		myResponse.Response = responseText
	} else {
		msg := tgbotapi.NewMessage(message.Chat.ID, "The message you're replying to does not contain text, a caption, or supported media.")
		_, _ = bot.Send(msg)
		return nil
	}

	// Marshal entities if available.
	var entitiesJSON string
	if len(myResponse.Entities) > 0 {
		bytes, err := json.Marshal(myResponse.Entities)
		if err != nil {
			log.Printf("Error marshalling entities: %v", err)
			entitiesJSON = ""
		} else {
			entitiesJSON = string(bytes)
		}
	}

	// Insert the single cascade trigger response entry.
	_, err = db.Exec(
		`INSERT INTO cascade_trigger_responses (cascade_trigger_id, response, file_type, file_id, file_name, entities)
         VALUES (?, ?, ?, ?, ?, ?)`,
		triggerID, myResponse.Response, myResponse.FileType, myResponse.FileID, myResponse.FileName, entitiesJSON)
	if err != nil {
		log.Printf("Error adding cascade trigger response: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, "Failed to add cascade trigger response.")
		_, _ = bot.Send(msg)
		return err
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "Cascade trigger added successfully!")
	_, _ = bot.Send(msg)
	return nil
}


// createMyResponse extracts response data from the replied message.
func createMyResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message) (MyResponse, error) {
	var myResponse MyResponse

	if message.ReplyToMessage.Caption != "" {
		myResponse.Response = message.ReplyToMessage.Caption
	} else {
		myResponse.Response = message.ReplyToMessage.Text
	}

	// In createMyResponse function
	if len(message.ReplyToMessage.Entities) > 0 {
		myResponse.Entities = filterCustomEmojiEntities(message.ReplyToMessage.Entities)
	} else if len(message.ReplyToMessage.CaptionEntities) > 0 {
		myResponse.Entities = filterCustomEmojiEntities(message.ReplyToMessage.CaptionEntities)
	} else {
		myResponse.Entities = []tgbotapi.MessageEntity{}
	}

	if len(message.ReplyToMessage.Photo) > 0 {
		photoFileID := message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
		myResponse.FileType = FilePhoto
		myResponse.FileID = photoFileID
	} else if message.ReplyToMessage.Animation != nil {
		gifFileID := message.ReplyToMessage.Animation.FileID
		myResponse.FileType = FileGIF
		myResponse.FileID = gifFileID
	} else if message.ReplyToMessage.Voice != nil {
		voiceFileID := message.ReplyToMessage.Voice.FileID
		myResponse.FileType = FileVoice
		myResponse.FileID = voiceFileID
	} else if message.ReplyToMessage.Sticker != nil {
		stickerFileID := message.ReplyToMessage.Sticker.FileID
		myResponse.FileType = FileSticker
		myResponse.FileID = stickerFileID
	} else if message.ReplyToMessage.Video != nil {
		videoFileID := message.ReplyToMessage.Video.FileID
		myResponse.FileType = FileVideo
		myResponse.FileID = videoFileID
	} else if message.ReplyToMessage.Document != nil {
		documentFileID := message.ReplyToMessage.Document.FileID
		myResponse.FileType = FileDocument
		myResponse.FileID = documentFileID
	} else if message.ReplyToMessage.Audio != nil {
		audioFileID := message.ReplyToMessage.Audio.FileID
		myResponse.FileType = FileAudio
		myResponse.FileID = audioFileID
	} else if message.ReplyToMessage.VideoNote != nil {
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

// handleCascadeTriggers retrieves and sends cascade trigger responses.
func handleCascadeTriggers(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	var messageContent string
	if message.Text != "" {
		messageContent = strings.ToLower(message.Text)
	} else if message.Caption != "" {
		messageContent = strings.ToLower(message.Caption)
	} else if len(message.Photo) > 0 || message.Animation != nil || message.Voice != nil ||
		message.Sticker != nil || message.Video != nil || message.Document != nil ||
		message.Audio != nil || message.VideoNote != nil {
		messageContent = ""
	} else {
		return nil
	}

	query := `
        SELECT 
            ct.id, 
            ct.search_phrase, 
            ctr.response, 
            ctr.file_type, 
            ctr.file_id, 
            ctr.file_name, 
            ctr.entities
        FROM cascade_triggers2 ct
        JOIN cascade_trigger_responses ctr ON ct.id = ctr.cascade_trigger_id
        WHERE ct.chat_id = ? AND LOWER(ct.search_phrase) = LOWER(?)
    `
	rows, err := db.Query(query, message.Chat.ID, messageContent)
	if err != nil {
		log.Printf("Error querying cascade triggers: %v", err)
		return err
	}
	defer rows.Close()

	var responses []MyResponse
	for rows.Next() {
		var response MyResponse
		var fileType, fileID, fileName string
		var entities sql.NullString

		err := rows.Scan(
			&response.ID,
			&response.SearchPhrase,
			&response.Response,
			&fileType,
			&fileID,
			&fileName,
			&entities,
		)
		if err != nil {
			log.Printf("Error scanning cascade trigger response: %v", err)
			continue
		}

		response.FileType = FileType(fileType)
		response.FileID = fileID
		response.FileName = fileName

		if entities.Valid && entities.String != "" {
			err := json.Unmarshal([]byte(entities.String), &response.Entities)
			if err != nil {
				log.Printf("Error unmarshalling entities: %v", err)
				response.Entities = []tgbotapi.MessageEntity{}
			}
		} else {
			response.Entities = []tgbotapi.MessageEntity{}
		}

		responses = append(responses, response)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating over cascade triggers: %v", err)
		return err
	}

	if len(responses) > 0 {
		for _, resp := range responses {
			chattableResponse, err := buildCascadeChattableResponse(message, resp)
			if err != nil {
				log.Printf("Error building chattable response for cascade trigger: %v", err)
				continue
			}

			_, err = bot.Send(chattableResponse)
			if err != nil {
				log.Printf("Error sending cascade trigger response: %v", err)
				continue
			}
		}
	}

	return nil
}

// handleRemoveCascadeCommand deletes a response from a cascade trigger.
func handleRemoveCascadeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	if message.ReplyToMessage == nil {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Пожалуйста, ответьте на сообщение, которое хотите удалить из каскадного триггера, и используйте команду /removec с указанием фразы триггера.\nПример: /removec Привет")
		_, _ = bot.Send(msg)
		return nil
	}

	triggerPhrase := strings.TrimSpace(message.CommandArguments())
	if triggerPhrase == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Пожалуйста, предоставьте ключевую фразу после команды /removec.\nПример: /removec Привет")
		_, _ = bot.Send(msg)
		return nil
	}
	triggerPhrase = strings.ToLower(triggerPhrase)

	var (
		responseText string
		fileID       string
	)
	if message.ReplyToMessage.Text != "" {
		responseText = message.ReplyToMessage.Text
	} else if message.ReplyToMessage.Caption != "" {
		responseText = message.ReplyToMessage.Caption
	}

	if len(message.ReplyToMessage.Photo) > 0 {
		fileID = message.ReplyToMessage.Photo[len(message.ReplyToMessage.Photo)-1].FileID
	} else if message.ReplyToMessage.Animation != nil {
		fileID = message.ReplyToMessage.Animation.FileID
	} else if message.ReplyToMessage.Voice != nil {
		fileID = message.ReplyToMessage.Voice.FileID
	} else if message.ReplyToMessage.Sticker != nil {
		fileID = message.ReplyToMessage.Sticker.FileID
	} else if message.ReplyToMessage.Video != nil {
		fileID = message.ReplyToMessage.Video.FileID
	} else if message.ReplyToMessage.Document != nil {
		fileID = message.ReplyToMessage.Document.FileID
	} else if message.ReplyToMessage.Audio != nil {
		fileID = message.ReplyToMessage.Audio.FileID
	} else if message.ReplyToMessage.VideoNote != nil {
		fileID = message.ReplyToMessage.VideoNote.FileID
	}

	if responseText == "" && fileID == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Сообщение, на которое вы отвечаете, не содержит текста, подписи или медиа.")
		_, _ = bot.Send(msg)
		return nil
	}

	log.Printf("Попытка удалить ответ. Триггер: '%s', Текст ответа: '%s', FileID: '%s', Чат: %d", triggerPhrase, responseText, fileID, message.Chat.ID)

	var triggerID int64
	err := db.QueryRow(`
        SELECT id FROM cascade_triggers2
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?)`,
		message.Chat.ID, triggerPhrase).Scan(&triggerID)
	if err != nil {
		if err == sql.ErrNoRows {
			msg := tgbotapi.NewMessage(message.Chat.ID, "Каскадный триггер с данной фразой не найден.")
			_, _ = bot.Send(msg)
			return nil
		}
		log.Printf("Ошибка при поиске ID каскадного триггера: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, "Произошла ошибка при попытке удалить ответ каскадного триггера.")
		_, _ = bot.Send(msg)
		return err
	}

	log.Printf("Найден triggerID: %d для фразы '%s'", triggerID, triggerPhrase)

	if fileID != "" {
		result, err := db.Exec(`
            DELETE FROM cascade_trigger_responses
            WHERE cascade_trigger_id = ? AND file_id = ?`,
			triggerID, fileID)
		if err != nil {
			log.Printf("Ошибка при удалении ответа каскадного триггера по file_id: %v", err)
			msg := tgbotapi.NewMessage(message.Chat.ID, "Не удалось удалить медиа-ответ каскадного триггера.")
			_, _ = bot.Send(msg)
			return err
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			msg := tgbotapi.NewMessage(message.Chat.ID, "Медиа-ответ с данным FileID не найден в указанном каскадном триггере.")
			_, _ = bot.Send(msg)
			return nil
		}

		log.Printf("Удалён медиа-ответ с FileID: %s из каскадного триггера '%s'", fileID, triggerPhrase)
		msgText := fmt.Sprintf("Медиа-ответ удалён из каскадного триггера '%s'.", triggerPhrase)
		msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
		_, _ = bot.Send(msg)
	} else {
		result, err := db.Exec(`
            DELETE FROM cascade_trigger_responses
            WHERE cascade_trigger_id = ? AND response = ?`,
			triggerID, responseText)
		if err != nil {
			log.Printf("Ошибка при удалении текстового ответа каскадного триггера: %v", err)
			msg := tgbotapi.NewMessage(message.Chat.ID, "Не удалось удалить текстовый ответ каскадного триггера.")
			_, _ = bot.Send(msg)
			return err
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			msg := tgbotapi.NewMessage(message.Chat.ID, "Текстовый ответ с данным содержимым не найден в указанном каскадном триггере.")
			_, _ = bot.Send(msg)
			return nil
		}

		log.Printf("Удалён текстовый ответ: '%s' из каскадного триггера '%s'", responseText, triggerPhrase)
		msgText := fmt.Sprintf("Текстовый ответ удалён из каскадного триггера '%s'.", triggerPhrase)
		msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
		_, _ = bot.Send(msg)
	}

	var remainingResponses int
	err = db.QueryRow(`
        SELECT COUNT(*) FROM cascade_trigger_responses
        WHERE cascade_trigger_id = ?`,
		triggerID).Scan(&remainingResponses)
	if err != nil {
		log.Printf("Ошибка при проверке оставшихся ответов: %v", err)
		return err
	}

	if remainingResponses == 0 {
		_, err = db.Exec(`
            DELETE FROM cascade_triggers2
            WHERE id = ?`,
			triggerID)
		if err != nil {
			log.Printf("Ошибка при удалении каскадного триггера: %v", err)
			msg := tgbotapi.NewMessage(message.Chat.ID, "Не удалось удалить каскадный триггер.")
			_, _ = bot.Send(msg)
			return err
		}
		msgText := fmt.Sprintf("Каскадный триггер '%s' удалён, так как больше не содержит ответов.", triggerPhrase)
		msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
		_, _ = bot.Send(msg)
	}

	return nil
}

func allowedMessageType(message *tgbotapi.Message) bool {
	if message.ReplyToMessage.Game != nil {
		return false
	}
	return true
}

func messageContains(messageText, targetString string) bool {
	normalizedMessage := norm.NFC.String(messageText)
	normalizedTarget := norm.NFC.String(targetString)
	lowercaseMessage := strings.ToLower(normalizedMessage)
	lowercaseTarget := strings.ToLower(normalizedTarget)
	return strings.Contains(lowercaseMessage, lowercaseTarget)
}

func messageMatches(messageText, targetString string) bool {
	normalizedMessage := strings.ToLower(norm.NFC.String(messageText))
	normalizedTarget := strings.ToLower(norm.NFC.String(targetString))
	return normalizedMessage == normalizedTarget
}

func handleRemoveCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	if message.From.ID == 89886125 {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Дима саси жопу")
		bot.Send(msg)
		return nil
	}
	removeSearchPhrase := strings.ToLower(message.CommandArguments())

	result, err := db.Exec(`
        DELETE FROM triggers
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
		message.Chat.ID, removeSearchPhrase, false)
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
	if message.From.ID != int64(193117018) {
		msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
		_, _ = bot.Send(msg)
		return nil
	}

	removeSearchPhrase := strings.ToLower(message.CommandArguments())

	result, err := db.Exec(`
        DELETE FROM triggers
        WHERE LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
		removeSearchPhrase, true)
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

	// Extract and normalize the trigger phrase
	newSearchPhrase := strings.TrimSpace(message.CommandArguments())
	if newSearchPhrase == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide a trigger phrase after the /add command.\nExample: /add Hello")
		_, _ = bot.Send(msg)
		return nil
	}
	newSearchPhrase = strings.ToLower(newSearchPhrase)

	// Check if a cascade trigger already exists; if so, do not allow local trigger creation.
	_, cascadeExists, err := checkTriggerExistence(db, message.Chat.ID, newSearchPhrase)
	if err != nil {
		log.Printf("Error checking trigger existence: %v", err)
		return err
	}
	if cascadeExists {
		msg := tgbotapi.NewMessage(message.Chat.ID, "A cascade trigger with this phrase already exists. Cannot create a normal trigger.")
		_, _ = bot.Send(msg)
		return nil
	}

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM triggers
         WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
		message.Chat.ID, newSearchPhrase, false).Scan(&count)
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

	var entitiesJSON string
	if len(newMyResponse.Entities) > 0 {
		newMyResponse.Entities = filterCustomEmojiEntities(newMyResponse.Entities)
		bytes, err := json.Marshal(newMyResponse.Entities)
		if err != nil {
			log.Printf("Error marshalling entities: %v", err)
			entitiesJSON = ""
		} else {
			entitiesJSON = string(bytes)
		}
	} else {
		entitiesJSON = ""
	}

	if count > 0 {
		_, err = db.Exec(
			`UPDATE triggers 
             SET response = ?, file_type = ?, file_id = ?, file_name = ?, entities = ?
             WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
			newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, entitiesJSON,
			message.Chat.ID, newMyResponse.SearchPhrase, false)
		if err != nil {
			log.Printf("Error updating trigger: %v", err)
			return err
		}

		msg := tgbotapi.NewMessage(message.Chat.ID, "Response updated!")
		_, _ = bot.Send(msg)
		return nil
	}

	_, err = db.Exec(
		`INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		message.Chat.ID, newMyResponse.SearchPhrase, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, entitiesJSON, false)
	if err != nil {
		log.Printf("Error inserting trigger: %v", err)
		return err
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New response added!")
	_, _ = bot.Send(msg)
	return nil
}

func handleAddGlobalCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	if message.From.ID != int64(193117018) {
		msg := tgbotapi.NewMessage(message.Chat.ID, "You are not authorized to use this command.")
		_, _ = bot.Send(msg)
		return nil
	}

	newSearchPhrase := strings.ToLower(message.CommandArguments())

	var count int
	err := db.QueryRow(`
        SELECT COUNT(*) FROM triggers
        WHERE LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
		newSearchPhrase, true).Scan(&count)
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

	var entitiesJSON string
	if len(newMyResponse.Entities) > 0 {
		newMyResponse.Entities = filterCustomEmojiEntities(newMyResponse.Entities)
		bytes, err := json.Marshal(newMyResponse.Entities)
		if err != nil {
			log.Printf("Error marshalling entities: %v", err)
			entitiesJSON = ""
		} else {
			entitiesJSON = string(bytes)
		}
	} else {
		entitiesJSON = ""
	}

	if count > 0 {
		_, err = db.Exec(`
            UPDATE triggers 
            SET response = ?, file_type = ?, file_id = ?, file_name = ?, entities = ?
            WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?`,
			newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, entitiesJSON, 0, newMyResponse.SearchPhrase, true)
		if err != nil {
			log.Printf("Error updating global trigger: %v", err)
			return err
		}

		msg := tgbotapi.NewMessage(message.Chat.ID, "Response updated!")
		_, _ = bot.Send(msg)
		return nil
	}

	_, err = db.Exec(`
        INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		0, newMyResponse.SearchPhrase, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, entitiesJSON, true)
	if err != nil {
		log.Printf("Error inserting global trigger: %v", err)
		return err
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "New global response added!")
	_, _ = bot.Send(msg)
	return nil
}

func processResponse(bot *tgbotapi.BotAPI, message *tgbotapi.Message, myResponse MyResponse) error {
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
	formattedText := myResponse.Response

	switch myResponse.FileType {
	case FilePhoto:
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		photoMsg.ReplyToMessageID = message.MessageID
		photoMsg.Caption = formattedText
		photoMsg.CaptionEntities = myResponse.Entities
		return photoMsg, nil

	case FileGIF:
		gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		gifMsg.ReplyToMessageID = message.MessageID
		gifMsg.Caption = formattedText
		return gifMsg, nil

	case FileVoice:
		voiceMsg := tgbotapi.NewVoice(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		voiceMsg.ReplyToMessageID = message.MessageID
		return voiceMsg, nil

	case FileSticker:
		stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		stickerMsg.ReplyToMessageID = message.MessageID
		return stickerMsg, nil

	case FileVideo:
		videoMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		videoMsg.ReplyToMessageID = message.MessageID
		videoMsg.Caption = formattedText
		videoMsg.CaptionEntities = myResponse.Entities
		return videoMsg, nil

	case FileDocument:
		documentMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		documentMsg.ReplyToMessageID = message.MessageID
		documentMsg.Caption = formattedText
		return documentMsg, nil

	case FileVideoNote:
		videoNoteMsg := tgbotapi.NewVideoNote(message.Chat.ID, 60, tgbotapi.FileID(myResponse.FileID))
		videoNoteMsg.ReplyToMessageID = message.MessageID
		return videoNoteMsg, nil

	case FileAudio:
		audioMsg := tgbotapi.NewAudio(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		audioMsg.ReplyToMessageID = message.MessageID
		audioMsg.Caption = formattedText
		return audioMsg, nil

	default:
		textMsg := tgbotapi.NewMessage(message.Chat.ID, formattedText)
		textMsg.ReplyToMessageID = message.MessageID
		textMsg.Entities = myResponse.Entities
		return textMsg, nil
	}
}

func buildCascadeChattableResponse(message *tgbotapi.Message, myResponse MyResponse) (tgbotapi.Chattable, error) {
	formattedText := myResponse.Response

	switch myResponse.FileType {
	case FilePhoto:
		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		photoMsg.Caption = formattedText
		photoMsg.CaptionEntities = myResponse.Entities
		return photoMsg, nil

	case FileGIF:
		gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		gifMsg.Caption = formattedText
		return gifMsg, nil

	case FileVoice:
		voiceMsg := tgbotapi.NewVoice(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		return voiceMsg, nil

	case FileSticker:
		stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		return stickerMsg, nil

	case FileVideo:
		videoMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		videoMsg.Caption = formattedText
		videoMsg.CaptionEntities = myResponse.Entities
		return videoMsg, nil

	case FileDocument:
		documentMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		documentMsg.Caption = formattedText
		return documentMsg, nil

	case FileVideoNote:
		videoNoteMsg := tgbotapi.NewVideoNote(message.Chat.ID, 60, tgbotapi.FileID(myResponse.FileID))
		videoNoteMsg.ReplyToMessageID = message.MessageID
		return videoNoteMsg, nil

	case FileAudio:
		audioMsg := tgbotapi.NewAudio(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
		audioMsg.Caption = formattedText
		return audioMsg, nil

	default:
		textMsg := tgbotapi.NewMessage(message.Chat.ID, formattedText)
		textMsg.Entities = myResponse.Entities
		return textMsg, nil
	}
}

func handleChatIDCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatid := "This chat ID is: " + strconv.FormatInt(message.Chat.ID, 10)
	msg := tgbotapi.NewMessage(message.Chat.ID, chatid)
	bot.Send(msg)
}

// --- Refactored roll-dice helper (Issue 9) ---
func rollDice(max int, bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	result := rand.Intn(max) + 1
	response := fmt.Sprintf("🎲 You rolled: %d", result)
	msg := tgbotapi.NewMessage(message.Chat.ID, response)
	msg.ReplyToMessageID = message.MessageID
	_, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending roll result: %v", err)
		return err
	}
	return nil
}

func handleRoll(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(100, bot, message)
}

func handleRoll20(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(20, bot, message)
}

func handleRoll12(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(12, bot, message)
}

func handleRoll10(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(10, bot, message)
}

func handleRoll8(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(8, bot, message)
}

func handleRoll6(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(6, bot, message)
}

func handleRoll4(bot *tgbotapi.BotAPI, message *tgbotapi.Message) error {
	return rollDice(4, bot, message)
}

// --- UTF-16 conversion helpers for applyEntitiesToText (Issue 3) ---
func utf16ToRuneIndices(text string) []int {
	runes := []rune(text)
	mapping := make([]int, len(runes))
	offset := 0
	for i, ru := range runes {
		mapping[i] = offset
		if ru > 0xFFFF {
			offset += 2
		} else {
			offset++
		}
	}
	return mapping
}

func codeUnitToRuneIndex(mapping []int, codeUnitOffset int) int {
	for i, off := range mapping {
		if off == codeUnitOffset {
			return i
		}
		if off > codeUnitOffset {
			return i
		}
	}
	return len(mapping)
}

func applyEntitiesToText(text string, entities []tgbotapi.MessageEntity) string {
	runes := []rune(text)
	mapping := utf16ToRuneIndices(text)

	type entityWithRuneIndices struct {
		entity tgbotapi.MessageEntity
		start  int
		end    int
	}
	var ents []entityWithRuneIndices
	for _, e := range entities {
		start := codeUnitToRuneIndex(mapping, e.Offset)
		end := codeUnitToRuneIndex(mapping, e.Offset+e.Length)
		ents = append(ents, entityWithRuneIndices{entity: e, start: start, end: end})
	}

	sort.Slice(ents, func(i, j int) bool {
		return ents[i].start > ents[j].start
	})

	for _, ent := range ents {
		before := string(runes[:ent.start])
		middle := string(runes[ent.start:ent.end])
		after := string(runes[ent.end:])
		switch ent.entity.Type {
		case "bold":
			middle = "**" + middle + "**"
		case "italic":
			middle = "*" + middle + "*"
		case "code":
			middle = "`" + middle + "`"
		case "pre":
			middle = "```" + middle + "```"
		case "url":
			url := ent.entity.URL
			middle = "[" + middle + "](" + url + ")"
		default:
			// Unsupported entity type.
		}
		newText := before + middle + after
		runes = []rune(newText)
		mapping = utf16ToRuneIndices(newText)
	}
	return string(runes)
}

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	receivedMessage := message.Text

	const targetChatID int64 = -1002245157577
	keyword := "рейдодроч"

	if message.Chat.ID == targetChatID && message.Text == keyword {
		// Check if 5 minutes have passed since the last occurrence.
		if !checkAndUpdateLastKeyword(targetChatID, keyword) {
			log.Println("Less than 5 minutes since the last occurrence of the keyword; skipping processing.")
			return nil // Do nothing
		}
	}

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
		if message.Chat.Type == "private"{
			if message.ForwardFrom != nil {
				senduserid := "<b>Message forwarded from User ID: </b> <code>" + strconv.FormatInt(message.ForwardFrom.ID, 10) + "</code>"
				msg := tgbotapi.NewMessage(message.Chat.ID, senduserid)
				msg.ParseMode = "HTML"
				bot.Send(msg)
				return nil
			} else  if message.ForwardSenderName != "" {
				msg := tgbotapi.NewMessage(message.Chat.ID, "<b>Sorry, this user's ID is hidden</b>")
				msg.ParseMode = "HTML"
				bot.Send(msg)
				return nil
			} else {
				senduserid := "<b>Your User ID:</b> <code>" + strconv.FormatInt(message.From.ID, 10) + "</code>"
				msg := tgbotapi.NewMessage(message.Chat.ID, senduserid)
				msg.ParseMode = "HTML"
				bot.Send(msg)
				return nil
			}
		} else {
			return nil
		}
	}

	commandHandlers := map[string]func(*tgbotapi.BotAPI, *tgbotapi.Message, *sql.DB) error{
		"add":          handleAddCommand,
		"remove":       handleRemoveCommand,
		"addglobal":    handleAddGlobalCommand,
		"triggers":     handleTriggersCommand,
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

	currentTime, _ := getCurrentTimeForLocation("America/Los_Angeles")
	currentTimeMoscow, _ := getCurrentTimeForLocation("Europe/Moscow")
	currentTimeNewYork, _ := getCurrentTimeForLocation("America/New_York")

	if (message.Chat.ID == -1001245934322 || message.Chat.ID == -1001390115843) &&
	strings.Contains(receivedMessage, "@Porky8888") && isTimeBetween(currentTime, 2, 7) {
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

	if message.Chat.ID == -1001970411651 && strings.Contains(receivedMessage, "@vincenitycarter") && isTimeBetween19And8(currentTimeMoscow) {
		fileID := "AgACAgQAAx0Cc2pGjQACAX9ltZ3416cTOKI_-1Jp1wXzAVCLygACG74xGwkasVEOQZYuKQ4abQEAAwIAA3kAAzUE"
		if rand.Float32() < 0.5 {
			fileID = "AgACAgIAAx0Cc2pGjQACAnVmeHhbXkkqgeg_DNEW1dChwB3BYQACuNoxG2g9yUsZaxbgiGFD_wEAAwIAA3kAAzUE"
		}

		photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(fileID))
		photoMsg.ReplyToMessageID = message.MessageID
		photoMsg.Caption = fmt.Sprintf("Сегодня, в %v, Яков Андреев был найден спящим в своей квартире. Приносим соболезнования всем его тиммейтам", currentTimeMoscow.Format("15:04"))
		bot.Send(photoMsg)
		return nil
	}

	if message.Chat.ID == -1002245157577 && strings.Contains(receivedMessage, "@KelThuzad") && isTimeBetween(currentTimeNewYork, 2, 7) {
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

	localRows, err := db.Query(`
        SELECT 
            id, 
            search_phrase, 
            response, 
            file_type, 
            file_id, 
            file_name,
            entities
        FROM triggers
        WHERE chat_id = ? AND is_global = ?`,
		message.Chat.ID, false)
	if err != nil {
		log.Printf("Error retrieving chat-specific triggers: %v", err)
		return err
	}
	defer localRows.Close()

	var chatSpecificTriggers []*MyResponse
	for localRows.Next() {
		var trigger MyResponse
		var response, fileType, fileID, fileName sql.NullString
		var entities sql.NullString

		err := localRows.Scan(
			&trigger.ID,
			&trigger.SearchPhrase,
			&response,
			&fileType,
			&fileID,
			&fileName,
			&entities,
		)
		if err != nil {
			log.Printf("Error scanning chat-specific trigger: %v", err)
			continue
		}

		trigger.Response = response.String
		trigger.FileType = FileType(fileType.String)
		trigger.FileID = fileID.String
		trigger.FileName = fileName.String

		if entities.Valid && entities.String != "" {
			err := json.Unmarshal([]byte(entities.String), &trigger.Entities)
			if err != nil {
				log.Printf("Error unmarshalling entities for chat-specific trigger ID %d: %v", trigger.ID, err)
				trigger.Entities = []tgbotapi.MessageEntity{}
			}
		} else {
			trigger.Entities = []tgbotapi.MessageEntity{}
		}

		chatSpecificTriggers = append(chatSpecificTriggers, &trigger)
	}

	if err := localRows.Err(); err != nil {
		log.Printf("Error iterating over chat-specific triggers: %v", err)
		return err
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
		globalRows, err := db.Query(`
            SELECT 
                id, 
                search_phrase, 
                response, 
                file_type, 
                file_id, 
                file_name,
                entities
            FROM triggers
            WHERE is_global = ?`,
			true)
		if err != nil {
			log.Printf("Error retrieving global triggers: %v", err)
			return err
		}
		defer globalRows.Close()

		var globalTriggers []*MyResponse
		for globalRows.Next() {
			var trigger MyResponse
			var response, fileType, fileID, fileName sql.NullString
			var entities sql.NullString

			err := globalRows.Scan(
				&trigger.ID,
				&trigger.SearchPhrase,
				&response,
				&fileType,
				&fileID,
				&fileName,
				&entities,
			)
			if err != nil {
				log.Printf("Error scanning global trigger: %v", err)
				continue
			}

			trigger.Response = response.String
			trigger.FileType = FileType(fileType.String)
			trigger.FileID = fileID.String
			trigger.FileName = fileName.String

			if entities.Valid && entities.String != "" {
				err := json.Unmarshal([]byte(entities.String), &trigger.Entities)
				if err != nil {
					log.Printf("Error unmarshalling entities for global trigger ID %d: %v", trigger.ID, err)
					trigger.Entities = []tgbotapi.MessageEntity{}
				}
			} else {
				trigger.Entities = []tgbotapi.MessageEntity{}
			}

			globalTriggers = append(globalTriggers, &trigger)
		}

		if err := globalRows.Err(); err != nil {
			log.Printf("Error iterating over global triggers: %v", err)
			return err
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

	err = handleCascadeTriggers(bot, message, db)
	if err != nil {
		log.Printf("Error handling cascade triggers: %v", err)
		return err
	}

/*	if message.From.ID == 578801 {
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
	}*/

	return nil
}

func handleTriggersCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	log.Println("Handling /triggers command")

	localRows, err := db.Query(`
        SELECT DISTINCT search_phrase
        FROM triggers
        WHERE chat_id = ? AND is_global = ?`,
		message.Chat.ID, false)
	if err != nil {
		log.Printf("Error retrieving chat-specific trigger phrases: %v", err)
		return err
	}
	defer localRows.Close()

	chatSpecificTriggers := []string{}
	for localRows.Next() {
		var searchPhrase string
		err := localRows.Scan(&searchPhrase)
		if err != nil {
			log.Printf("Error scanning chat-specific trigger phrase: %v", err)
			continue
		}
		chatSpecificTriggers = append(chatSpecificTriggers, searchPhrase)
	}

	globalRows, err := db.Query(`
        SELECT DISTINCT search_phrase
        FROM triggers
        WHERE is_global = ?`,
		true)
	if err != nil {
		log.Printf("Error retrieving global trigger phrases: %v", err)
		return err
	}
	defer globalRows.Close()

	globalTriggers := []string{}
	for globalRows.Next() {
		var searchPhrase string
		err := globalRows.Scan(&searchPhrase)
		if err != nil {
			log.Printf("Error scanning global trigger phrase: %v", err)
			continue
		}
		globalTriggers = append(globalTriggers, searchPhrase)
	}

	cascadeRows, err := db.Query(`
        SELECT DISTINCT search_phrase
        FROM cascade_triggers2
        WHERE chat_id = ?`,
		message.Chat.ID)
	if err != nil {
		log.Printf("Error retrieving cascade trigger phrases: %v", err)
		return err
	}
	defer cascadeRows.Close()

	cascadeTriggers := []string{}
	for cascadeRows.Next() {
		var searchPhrase string
		err := cascadeRows.Scan(&searchPhrase)
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

	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	bot.Send(msg)
	err = os.Remove(filePath)
	if err != nil {
		log.Printf("Failed to delete file: %s, error: %v\n", filePath, err)
	}
}

func sanitizeFileName(fileName string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9.-]`)
	return re.ReplaceAllString(fileName, "_")
}

func handleGenerateBarcode(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	code := message.CommandArguments()
	chatID := message.Chat.ID
	filePath := fmt.Sprintf("./temp/%s.png", code)

	bar, err := code128.Encode(code)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to generate barcode.")
		msg.ReplyToMessageID = message.MessageID
		bot.Send(msg)
		return
	}

	scaledBarcode, err := barcode.Scale(bar, 300, 100)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to scale barcode.")
		msg.ReplyToMessageID = message.MessageID
		bot.Send(msg)
		return
	}

	file, err := os.Create(filePath)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to create file.")
		msg.ReplyToMessageID = message.MessageID
		bot.Send(msg)
		return
	}
	defer file.Close()

	err = png.Encode(file, scaledBarcode)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to encode barcode.")
		msg.ReplyToMessageID = message.MessageID
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	bot.Send(msg)
	err = os.Remove(filePath)
	if err != nil {
		log.Printf("Failed to delete file: %s, error: %v\n", filePath, err)
	}
}

func handleSampleSize(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	commandText := message.Text
	riskCategory, populationSize, err := parseCommandArguments(commandText)
	if err != nil {
		fmt.Println(err)
		return
	}

	sampleSize, err := GetSampleSize(populationSize, riskCategory)
	if err != nil {
		fmt.Println(err)
		return
	}

	randomSelection := GenerateRandomSelection(sampleSize, populationSize)
	messageText := fmt.Sprintf("For %s risk and population of %d, sample size is %d\n", riskCategory, populationSize, sampleSize)
	messageText += "Random numbers for random selection: \n"
	for _, num := range randomSelection {
		messageText += fmt.Sprintf("%d \n", num)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, messageText)
	bot.Send(msg)
}

func parseCommandArguments(commandText string) (string, int, error) {
	parts := strings.Fields(commandText)
	if len(parts) < 3 {
		return "", 0, errors.New("invalid command format")
	}
	risk := parts[1]
	populationStr := parts[2]
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
	if sampleSize > population {
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
	sort.Ints(selection)
	return selection
}

func handleTerpetMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
	if message.Chat.ID == -1001390115843 && (message.Text == "Терпеть" || message.Text == "/terpet") {
		userID := message.From.ID
		username := message.From.UserName
		firstName := message.From.FirstName

		_, err := db.Exec(`
            INSERT INTO terpet_count (user_id, username, first_name, count)
            VALUES (?, ?, ?, 1)
            ON CONFLICT(user_id) DO UPDATE SET count = count + 1, username = ?, first_name = ?`,
			userID, username, firstName, username, firstName)
		if err != nil {
			log.Printf("Error updating terpet count: %v", err)
			return err
		}

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
        LIMIT 5`)
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
	} else if (count%10 >= 2 && count%10 <= 4) && !(count%100 >= 12 && count%100 <= 14) {
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
		stickerSetName := "privetcivpack_by_fStikBot"
		config := tgbotapi.GetStickerSetConfig{
			Name: stickerSetName,
		}
		stickerSet, err := bot.GetStickerSet(config)
		if err != nil {
			log.Printf("Error getting sticker set: %v", err)
			return err
		}

		randIndex := rand.Intn(len(stickerSet.Stickers))
		randomSticker := stickerSet.Stickers[randIndex]

		stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(randomSticker.FileID))
		_, err = bot.Send(stickerMsg)
		if err != nil {
			log.Printf("Error sending sticker: %v", err)
			return err
		}
	}
	return nil
}

// Filter out custom emoji entities which may not be supported by the current API version
func filterCustomEmojiEntities(entities []tgbotapi.MessageEntity) []tgbotapi.MessageEntity {
    filteredEntities := []tgbotapi.MessageEntity{}
    for _, entity := range entities {
        // Skip custom_emoji type entities to avoid the "Can't find field 'custom_emoji_id'" error
        if entity.Type != "custom_emoji" {
            filteredEntities = append(filteredEntities, entity)
        }
    }
    return filteredEntities
}

// checkAndUpdateLastKeyword checks if at least 5 minutes have passed since the last time
// 'keyword' was processed in the given chatID. It returns true if processing should continue.
func checkAndUpdateLastKeyword(chatID int64, keyword string) bool {
	key := formatKey(chatID, keyword)
	now := time.Now()

	timestampsMu.Lock()
	defer timestampsMu.Unlock()

	lastTime, exists := lastKeywordTimestamps[key]
	if exists && now.Sub(lastTime) < 5*time.Minute {
		// Not enough time has passed.
		return false
	}
	// Update the timestamp and allow processing.
	lastKeywordTimestamps[key] = now
	return true
}

func formatKey(chatID int64, keyword string) string {
	return string(chatID) + ":" + keyword
}