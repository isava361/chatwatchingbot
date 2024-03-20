package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"time"
)

type FileType string

const FilePhoto FileType = "photo"
const FileGIF FileType = "gif"
const FileSticker FileType = "sticker"
const FileVoice FileType = "voice"
const FileVideo FileType = "video"
const FileDocument FileType = "document"
const FileVideoNote FileType = "videonote"
const FileAudio FileType = "audio"

type MyResponse struct {
    ID           int64    `json:"id"`
    SearchPhrase string   `json:"searchPhrase"`
    Response     string   `json:"response,omitempty"`
    FileType     FileType `json:"fileType,omitempty"`
    FileID       string   `json:"fileID,omitempty"`
    FileName     string   `json:"filename,omitempty"`
}


func main() {

	token, err := readBotToken("./config/token.txt")
	if err != nil {
		log.Panicf("Token error: ", err)
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)
	
	db, err := sql.Open("sqlite3", "./mydb.db")
	if err != nil {
		log.Println(err)
		return
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS timezones (chatID INTEGER, location TEXT, PRIMARY KEY (chatID, location))`)
	if err != nil {
		log.Println(err)
		return
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS messagelist (chatID INTEGER PRIMARY KEY, messageID INTEGER)`)
	if err != nil {
		log.Println(err)
		return
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS alias (chatID INTEGER, location TEXT, alias TEXT)`)
	if err != nil {
		log.Println(err)
		return
	}

	_, err = db.Exec(`
    CREATE TABLE IF NOT EXISTS triggers (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        chat_id INTEGER,
        search_phrase TEXT,
        response TEXT,
        file_type TEXT,
        file_id TEXT,
        file_name TEXT,
        is_global BOOLEAN
    	)
	`)
	if err != nil {
    	log.Fatalf("Error creating triggers table: %v", err)
	}

	_, err = db.Exec(`
    CREATE TABLE IF NOT EXISTS terpet_count (
        user_id INTEGER PRIMARY KEY,
        username TEXT,
        first_name TEXT,
        count INTEGER DEFAULT 0
    )
	`)
	if err != nil {
		log.Fatalf("Error creating terpet_count table: %v", err)
	}

	chatIDs, err := getAllActiveChatIDs(db)
	if err != nil {
		log.Printf("Error getting active chats: %v", err)
	}
	for _, chatID := range chatIDs {
		updateTimeMessage(bot, chatID, db)
	}

   // Start a ticker that triggers every 30 seconds
   ticker := time.NewTicker(30 * time.Second)
   go func() {
	for range ticker.C {
		chatIDs, err := getAllActiveChatIDs(db)
		if err != nil {
			log.Printf("Error getting active chats: %v", err)
			continue
		}
		for _, chatID := range chatIDs {
			updateTimeMessage(bot, chatID, db)
		}
	}
   }()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)
	

	for update := range updates {

		if update.Message != nil {
			m := update.Message
			if m.From == nil {
				continue
			}

			log.Printf("Message received")
			err := handleMessage(bot, m, db)
			if err != nil {
				log.Printf("[%s] %s,   err: %s", update.Message.From.UserName, update.Message.Text, err.Error())
				continue
			}

			log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		}
	}
}
