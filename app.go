package main

import (
	"github.com/fsnotify/fsnotify"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"sync"
)

type FileType string

const FilePhoto FileType = "photo"
const FileGIF FileType = "gif"
const FileSticker FileType = "sticker"
const FileVoice FileType = "voice"

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

var File = &FileWriter{FileName: configlocation}

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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Panicf("Error creating file watcher: %v", err)
	}
	defer watcher.Close()

	// Add the configuration file to the watcher
	err = watcher.Add(configlocation)
	if err != nil {
		log.Panicf("Error adding file to watcher: %v", err)
	}


	// Goroutine to handle configuration file changes
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Remove == fsnotify.Remove {
					log.Println("Config file changed, reloading...")
					config, err = readConfig(configlocation)
					if err != nil {
						log.Printf("Error reading config: %v", err)
					}
				}
			case err := <-watcher.Errors:
				log.Println("Error in file watcher:", err)
			}
		}
	}()

	for update := range updates {

		if update.Message != nil {
			m := update.Message
			if m.From == nil {
				continue
			}

			log.Printf("Message received")
			err := handleMessage(bot, m, config)
			if err != nil {
				log.Printf("[%s] %s,   err: %s", update.Message.From.UserName, update.Message.Text, err.Error())
				continue
			}

			log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		}
	}
}
