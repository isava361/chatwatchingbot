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
"unicode/utf8"
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

func checkTriggerExistence(db *sql.DB, chatID int64, searchPhrase string) (bool, bool, error) {
    var normalCount, cascadeCount int
    // Convert searchPhrase to lowercase for consistent comparison
    searchPhrase = strings.ToLower(searchPhrase)

    err := db.QueryRow(`
        SELECT COUNT(*) FROM triggers 
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?
    `, chatID, searchPhrase, false).Scan(&normalCount)

    if err != nil {
        return false, false, err
    }

    err = db.QueryRow(`
        SELECT COUNT(*) FROM cascade_triggers2 
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?)
    `, chatID, searchPhrase).Scan(&cascadeCount)

    if err != nil {
        return false, false, err
    }

    return normalCount > 0, cascadeCount > 0, nil
}


// Обновленная функция handleAddCascadeCommand
func handleAddCascadeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    // Проверка, что команда была отправлена в ответ на сообщение
    if message.ReplyToMessage == nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Пожалуйста, ответьте на сообщение, которое хотите использовать в качестве ответа при добавлении каскадного триггера.\nПример: /addc Привет")
        _, _ = bot.Send(msg)
        return nil
    }

    // Извлечение ключевой фразы из аргументов команды
    triggerPhrase := strings.TrimSpace(message.CommandArguments())
    if triggerPhrase == "" {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Пожалуйста, предоставьте ключевую фразу после команды /addc.\nПример: /addc Привет")
        _, _ = bot.Send(msg)
        return nil
    }

    // Проверка существования каскадного триггера с такой же фразой
    var existingTriggerID int64
    err := db.QueryRow(`
        SELECT id FROM cascade_triggers2
        WHERE chat_id = ? AND search_phrase = ?
    `, message.Chat.ID, triggerPhrase).Scan(&existingTriggerID)

    if err != nil && err != sql.ErrNoRows {
        log.Printf("Ошибка при проверке существования триггера: %v", err)
        return err
    }

    var triggerID int64
    if err == sql.ErrNoRows {
        result, err := db.Exec(`
            INSERT INTO cascade_triggers2 (chat_id, search_phrase)
            VALUES (?, ?)
        `, message.Chat.ID, triggerPhrase)
        if err != nil {
            log.Printf("Error adding cascade trigger: %v", err)
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
        triggerID = existingTriggerID
    }

    // Create a response from the replied message with formatting
    myResponse, err := createMyResponse(bot, message)
    if err != nil {
        log.Printf("Error creating MyResponse: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Не получилось добавить каскадный триггер")
        bot.Send(msg)
        return err
    }
    myResponse.SearchPhrase = triggerPhrase

    _, err = db.Exec(`
        INSERT INTO cascade_trigger_responses (cascade_trigger_id, response, file_type, file_id, file_name, parse_mode)
        VALUES (?, ?, ?, ?, ?, ?)
    `, triggerID, myResponse.Response, myResponse.FileType, myResponse.FileID, myResponse.FileName, myResponse.ParseMode)
    if err != nil {
        log.Printf("Error adding cascade response: %v", err)
        msg := tgbotapi.NewMessage(message.Chat.ID, "Не получилось добавить каскадный триггер.")
        _, _ = bot.Send(msg)
        return err
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, "Каскадный триггер успешно добавлен!")
    _, _ = bot.Send(msg)
    return nil
}





// Обновлённая функция createMyResponseFromCascade
func createMyResponseFromCascade(bot *tgbotapi.BotAPI, repliedMessage *tgbotapi.Message) (MyResponse, error) {
    var myResponse MyResponse

    // Извлечение текста или подписи, если они есть
    if repliedMessage.Text != "" {
        myResponse.Response = repliedMessage.Text
    } else if repliedMessage.Caption != "" {
        myResponse.Response = repliedMessage.Caption
    }

    // Определение типа медиа и извлечение file_id, если медиа присутствует
    switch {
    case len(repliedMessage.Photo) > 0:
        myResponse.FileType = FilePhoto
        myResponse.FileID = repliedMessage.Photo[len(repliedMessage.Photo)-1].FileID
    case repliedMessage.Animation != nil:
        myResponse.FileType = FileGIF
        myResponse.FileID = repliedMessage.Animation.FileID
    case repliedMessage.Voice != nil:
        myResponse.FileType = FileVoice
        myResponse.FileID = repliedMessage.Voice.FileID
    case repliedMessage.Sticker != nil:
        myResponse.FileType = FileSticker
        myResponse.FileID = repliedMessage.Sticker.FileID
    case repliedMessage.Video != nil:
        myResponse.FileType = FileVideo
        myResponse.FileID = repliedMessage.Video.FileID
    case repliedMessage.Document != nil:
        myResponse.FileType = FileDocument
        myResponse.FileID = repliedMessage.Document.FileID
    case repliedMessage.Audio != nil:
        myResponse.FileType = FileAudio
        myResponse.FileID = repliedMessage.Audio.FileID
    case repliedMessage.VideoNote != nil:
        myResponse.FileType = FileVideoNote
        myResponse.FileID = repliedMessage.VideoNote.FileID
    default:
        myResponse.FileType = ""
    }

    return myResponse, nil
}

// Обновлённая функция handleCascadeTriggers
func handleCascadeTriggers(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    // Определяем содержимое сообщения: текст или подпись к медиа
    var messageContent string
    if message.Text != "" {
        messageContent = strings.ToLower(message.Text)
    } else if message.Caption != "" {
        messageContent = strings.ToLower(message.Caption)
    } else if len(message.Photo) > 0 || message.Animation != nil || message.Voice != nil ||
        message.Sticker != nil || message.Video != nil || message.Document != nil ||
        message.Audio != nil || message.VideoNote != nil {
        // Если сообщение содержит медиа без текста и подписи, используем пустую строку как content
        messageContent = ""
    } else {
        // Если нет текста, подписи и медиа, то триггер не срабатывает
        return nil
    }

    // Получение каскадных триггеров для данного чата, соответствующих полученному сообщению
    // Если messageContent пустая, ищем триггеры с пустой search_phrase (если такие есть)
    rows, err := db.Query(`
        SELECT ct.id, ct.search_phrase, ctr.response, ctr.file_type, ctr.file_id, ctr.file_name
        FROM cascade_triggers2 ct
        JOIN cascade_trigger_responses ctr ON ct.id = ctr.cascade_trigger_id
        WHERE ct.chat_id = ? AND ct.search_phrase = ?
    `, message.Chat.ID, messageContent)

    if err != nil {
        log.Printf("Error querying cascade triggers: %v", err)
        return err
    }
    defer rows.Close()

    var responses []MyResponse
    for rows.Next() {
        var response MyResponse
        var fileType, fileID, fileName sql.NullString
        err := rows.Scan(&response.ID, &response.SearchPhrase, &response.Response, &fileType, &fileID, &fileName)
        if err != nil {
            log.Printf("Error scanning cascade trigger response: %v", err)
            continue
        }
        response.FileType = FileType(fileType.String)
        response.FileID = fileID.String
        response.FileName = fileName.String
        responses = append(responses, response)
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


// Обновлённая функция handleRemoveCascadeCommand
func handleRemoveCascadeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) error {
    // Проверка, что команда была отправлена в ответ на сообщение
    if message.ReplyToMessage == nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Пожалуйста, ответьте на сообщение, которое хотите удалить из каскадного триггера, и используйте команду /removec с указанием фразы триггера.\nПример: /removec Привет")
        _, _ = bot.Send(msg)
        return nil
    }

    // Извлечение ключевой фразы из аргументов команды
    triggerPhrase := strings.TrimSpace(message.CommandArguments())
    if triggerPhrase == "" {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Пожалуйста, предоставьте ключевую фразу после команды /removec.\nПример: /removec Привет")
        _, _ = bot.Send(msg)
        return nil
    }

    // Извлечение содержимого сообщения, на которое отвечают
    var (
        responseText string
        fileID       string
    )

    // Определение типа сообщения: текстовое или медиа
    if message.ReplyToMessage.Text != "" {
        responseText = message.ReplyToMessage.Text
    } else if message.ReplyToMessage.Caption != "" {
        responseText = message.ReplyToMessage.Caption
    }

    // Проверка наличия медиа в сообщении
    if len(message.ReplyToMessage.Photo) > 0 {
        // Для фото используем FileID последнего размера
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

    // Поиск ID каскадного триггера по фразе (без изменения регистра)
    var triggerID int64
    err := db.QueryRow(`
        SELECT id FROM cascade_triggers2
        WHERE chat_id = ? AND search_phrase = ?
    `, message.Chat.ID, triggerPhrase).Scan(&triggerID)

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

    // Поиск и удаление ответа в зависимости от типа сообщения
    if fileID != "" {
        // Если сообщение содержит медиа, ищем по file_id
        result, err := db.Exec(`
            DELETE FROM cascade_trigger_responses
            WHERE cascade_trigger_id = ? AND file_id = ?
        `, triggerID, fileID)
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
        // Если сообщение текстовое, ищем по response
        result, err := db.Exec(`
            DELETE FROM cascade_trigger_responses
            WHERE cascade_trigger_id = ? AND response = ?
        `, triggerID, responseText)
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

    // Проверка, остались ли ещё ответы в триггере
    var remainingResponses int
    err = db.QueryRow(`
        SELECT COUNT(*) FROM cascade_trigger_responses
        WHERE cascade_trigger_id = ?
    `, triggerID).Scan(&remainingResponses)
    if err != nil {
        log.Printf("Ошибка при проверке оставшихся ответов: %v", err)
        return err
    }

    // Если ответов больше нет, удалить сам триггер
    if remainingResponses == 0 {
        _, err = db.Exec(`
            DELETE FROM cascade_triggers2
            WHERE id = ?
        `, triggerID)
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

func messageMatches(messageText, targetString string) bool {
    // Normalize both strings to NFC form and convert to lowercase
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
    // Convert removeSearchPhrase to lowercase
    removeSearchPhrase := strings.ToLower(message.CommandArguments())

    // Delete the trigger from the database using case-insensitive comparison
    result, err := db.Exec(`
        DELETE FROM triggers
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?
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

    // Convert removeSearchPhrase to lowercase
    removeSearchPhrase := strings.ToLower(message.CommandArguments())

    // Delete the global trigger from the database using case-insensitive comparison
    result, err := db.Exec(`
        DELETE FROM triggers
        WHERE LOWER(search_phrase) = LOWER(?) AND is_global = ?
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

    newSearchPhrase := strings.ToLower(message.CommandArguments())

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
    err = db.QueryRow(`
        SELECT COUNT(*) FROM triggers
        WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?
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
        _, err = db.Exec(`
            UPDATE triggers 
            SET response = ?, file_type = ?, file_id = ?, file_name = ?, parse_mode = ?
            WHERE chat_id = ? AND LOWER(search_phrase) = LOWER(?) AND is_global = ?
        `, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, newMyResponse.ParseMode, message.Chat.ID, newMyResponse.SearchPhrase, false)
        if err != nil {
            log.Printf("Error updating trigger: %v", err)
            return err
        }

        msg := tgbotapi.NewMessage(message.Chat.ID, "Response updated!")
        _, _ = bot.Send(msg)
        return nil
    }

    _, err = db.Exec(`
        INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, is_global, parse_mode)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `, message.Chat.ID, newMyResponse.SearchPhrase, newMyResponse.Response, newMyResponse.FileType, newMyResponse.FileID, newMyResponse.FileName, false, newMyResponse.ParseMode)
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
	var rawText string
	var entities []tgbotapi.MessageEntity

	// Decide where to get the text from
	if message.ReplyToMessage.Caption != "" {
		rawText = message.ReplyToMessage.Caption
		entities = message.ReplyToMessage.CaptionEntities
	} else {
		rawText = message.ReplyToMessage.Text
		entities = message.ReplyToMessage.Entities
	}

	// Convert text + entities to HTML
	formattedText := rawText
	if len(entities) > 0 {
		formattedText = entitiesToHTML(rawText, entities)
	}

	myResponse.Response = formattedText
	myResponse.ParseMode = "HTML" // We are using HTML formatting

	// Identify file type (if any)
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
		myResponse.FileName = message.ReplyToMessage.Document.FileName
	} else if message.ReplyToMessage.Audio != nil {
		audioFileID := message.ReplyToMessage.Audio.FileID
		myResponse.FileType = FileAudio
		myResponse.FileID = audioFileID
		myResponse.FileName = message.ReplyToMessage.Audio.FileName
	} else if message.ReplyToMessage.VideoNote != nil {
		videonoteFileID := message.ReplyToMessage.VideoNote.FileID
		myResponse.FileType = FileVideoNote
		myResponse.FileID = videonoteFileID
	} else if !allowedMessageType(message) {
		return myResponse, fmt.Errorf("Unsupported message type: %v", message)
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
    response := myResponse.Response
    if !utf8.ValidString(response) {
        response = strings.ToValidUTF8(response, "")
    }
    myResponse.Response = response
    
    parseMode := myResponse.ParseMode // use stored parse mode, typically "HTML"

    switch myResponse.FileType {
    case FilePhoto:
        photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        photoMsg.Caption = myResponse.Response
        photoMsg.ParseMode = parseMode
        photoMsg.ReplyToMessageID = message.MessageID
        return photoMsg, nil
    case FileGIF:
        gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        gifMsg.Caption = myResponse.Response
        gifMsg.ParseMode = parseMode
        gifMsg.ReplyToMessageID = message.MessageID
        return gifMsg, nil
    case FileVoice:
        voiceMsg := tgbotapi.NewVoice(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        voiceMsg.ParseMode = parseMode
        voiceMsg.ReplyToMessageID = message.MessageID
        return voiceMsg, nil
    case FileSticker:
        stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        stickerMsg.ReplyToMessageID = message.MessageID
        return stickerMsg, nil
    case FileVideo:
        videoMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        videoMsg.Caption = myResponse.Response
        videoMsg.ParseMode = parseMode
        videoMsg.ReplyToMessageID = message.MessageID
        return videoMsg, nil
    case FileDocument:
        documentMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        documentMsg.Caption = myResponse.Response
        documentMsg.ParseMode = parseMode
        documentMsg.ReplyToMessageID = message.MessageID
        return documentMsg, nil
    case FileVideoNote:
        videonoteMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        videonoteMsg.ParseMode = parseMode
        videonoteMsg.ReplyToMessageID = message.MessageID
        return videonoteMsg, nil
    case FileAudio:
        audioMsg := tgbotapi.NewAudio(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        audioMsg.Caption = myResponse.Response
        audioMsg.ParseMode = parseMode
        audioMsg.ReplyToMessageID = message.MessageID
        return audioMsg, nil
    default:
        textMsg := tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
        textMsg.ParseMode = parseMode
        textMsg.ReplyToMessageID = message.MessageID
        return textMsg, nil
    }
}


func buildCascadeChattableResponse(message *tgbotapi.Message, myResponse MyResponse) (tgbotapi.Chattable, error) {
    response := myResponse.Response
    if !utf8.ValidString(response) {
        response = strings.ToValidUTF8(response, "")
    }
    myResponse.Response = response
    
    parseMode := myResponse.ParseMode

    switch myResponse.FileType {
    case FilePhoto:
        photoMsg := tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        photoMsg.Caption = myResponse.Response
        photoMsg.ParseMode = parseMode
        return photoMsg, nil
    case FileGIF:
        gifMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        gifMsg.Caption = myResponse.Response
        gifMsg.ParseMode = parseMode
        return gifMsg, nil
    case FileVoice:
        voiceMsg := tgbotapi.NewVoice(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        voiceMsg.ParseMode = parseMode
        return voiceMsg, nil
    case FileSticker:
        stickerMsg := tgbotapi.NewSticker(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        return stickerMsg, nil
    case FileVideo:
        videoMsg := tgbotapi.NewVideo(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        videoMsg.Caption = myResponse.Response
        videoMsg.ParseMode = parseMode
        return videoMsg, nil
    case FileDocument:
        documentMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        documentMsg.Caption = myResponse.Response
        documentMsg.ParseMode = parseMode
        return documentMsg, nil
    case FileVideoNote:
        videonoteMsg := tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        videonoteMsg.ParseMode = parseMode
        return videonoteMsg, nil
    case FileAudio:
        audioMsg := tgbotapi.NewAudio(message.Chat.ID, tgbotapi.FileID(myResponse.FileID))
        audioMsg.Caption = myResponse.Response
        audioMsg.ParseMode = parseMode
        return audioMsg, nil
    default:
        textMsg := tgbotapi.NewMessage(message.Chat.ID, myResponse.Response)
        textMsg.ParseMode = parseMode
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


//    if message.From.ID == 89886125 {
//        return nil
//    }
    
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

    // Process cascade triggers (Case-Insensitive)
    err = handleCascadeTriggers(bot, message, db)
    if err != nil {
        log.Printf("Error handling cascade triggers: %v", err)
        return err
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
        FROM cascade_triggers2
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

func entitiesToHTML(text string, entities []tgbotapi.MessageEntity) string {
    sort.Slice(entities, func(i, j int) bool {
        return entities[i].Offset < entities[j].Offset
    })

    result := text
    for i := len(entities) - 1; i >= 0; i-- {
        e := entities[i]
        start := e.Offset
        end := e.Offset + e.Length
        if end > len(result) {
            end = len(result)
        }

        segment := result[start:end]

        switch e.Type {
        case "bold":
            segment = fmt.Sprintf("<b>%s</b>", segment)
        case "italic":
            segment = fmt.Sprintf("<i>%s</i>", segment)
        case "underline":
            segment = fmt.Sprintf("<u>%s</u>", segment)
        case "strikethrough":
            segment = fmt.Sprintf("<s>%s</s>", segment)
        case "code":
            segment = fmt.Sprintf("<code>%s</code>", segment)
        case "pre":
            segment = fmt.Sprintf("<pre>%s</pre>", segment)
        case "text_link":
            // e.URL is a string, not a pointer
            if e.URL != "" {
                segment = fmt.Sprintf("<a href='%s'>%s</a>", e.URL, segment)
            }
        case "text_mention":
            if e.User != nil {
                segment = fmt.Sprintf("<a href='tg://user?id=%d'>%s</a>", e.User.ID, segment)
            }
        }

        result = result[:start] + segment + result[end:]
    }

    return result
}
