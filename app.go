package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"sync"
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
	SearchPhrase string   `json:"searchPhrase"`
	Response     string   `json:"response,omitempty"`
	FileType     FileType `json:"fileType,omitempty"`
	FileID       string   `json:"fileID,omitempty"`
	FileName     string   `json:"filename,omitempty"`
}

type Config struct {
	MyResponses  []MyResponse           `json:"myResponses"`
	ChatTriggers map[int64][]MyResponse `json:"chat_triggers,omitempty"`
}

type ConfigWriter interface {
	Get(key string) (string, error)
	Put(config *Config) error
}
  
type FileWriter struct {
	FileName string
	mutex sync.Mutex
}

const configlocation = "./config/config.json"

func main() {
	config, err := readConfig(configlocation)
	if err != nil {
		log.Panicf("Config error: %v", err)
	}

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

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS alias (chatID INTEGER PRIMARY KEY, location TEXT, alias TEXT)`)
	if err != nil {
		log.Println(err)
		return
	}

   // Start a ticker that triggers every 5 minutes
   ticker := time.NewTicker(5 * time.Minute)
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
	
	var cf ConfigWriter
	configUpdate := make(chan *Config)
	cf, configUpdate = NewFileWriter(config, configlocation)

	go func() {
		for newConfig := range configUpdate {
			config = newConfig
		}
	}()

	for update := range updates {

		if update.Message != nil {
			m := update.Message
			if m.From == nil {
				continue
			}

			log.Printf("Message received")
			err := handleMessage(bot, m, config, cf, db)
			if err != nil {
				log.Printf("[%s] %s,   err: %s", update.Message.From.UserName, update.Message.Text, err.Error())
				continue
			}

			log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		}
	}
}
