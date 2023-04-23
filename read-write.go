package main

import (
//	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"encoding/json"
//	"errors"
	"io/ioutil"
//	"time"
//	"log"
//	"fmt"
//	"sort"
)

func readConfig(filename string) (Config, error) {
	var config Config

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return config, err
	}

	err = json.Unmarshal(data, &config)
	if err != nil {
		return config, err
	}

	return config, nil
}
/*
func addChatToAllowedChats(config *Config, chatID int64) error {
	config.AllowedChats[chatID] = true
	return writeConfig("test-config.json", *config)
}

func addChannelToWhitelist(config *Config, channel string) error {
	config.Whitelist[channel] = true
	return writeConfig("test-config.json", *config)
}

func removeChatFromAllowedChats(config *Config, chatID int64) error {
	allowedChats := config.AllowedChats

	if _, ok := allowedChats[chatID]; ok {
		delete(allowedChats, chatID)
		config.AllowedChats = allowedChats
		err := writeConfig("test-config.json", *config)
		if err != nil {
			return err
		}
		return nil
	}
	return errors.New("can't remove channel")
}

func removeFromWhitelist(config *Config, channelToRemove string) error {
	whitelist := config.Whitelist

	if _, ok := whitelist[channelToRemove]; ok {
		delete(whitelist, channelToRemove)
		config.Whitelist = whitelist
		err := writeConfig("test-config.json", *config)
		if err != nil {
			return err
		}
		return nil
	}
	return errors.New("channel not found in whitelist")
}

func addTempChat(config *Config, chatName string, duration int, dateTimeAdded time.Time) {
	tempChat := TempChat{
		ChatName:       chatName,
		TimeAdded:       duration,
		DateTimeAdded:  dateTimeAdded,
	}
	config.TempChats = append(config.TempChats, tempChat)
	writeConfig("test-config.json", *config)
}

func checkTempChats(bot *tgbotapi.BotAPI, config *Config) {
	for {
		time.Sleep(30 * time.Second)

		now := time.Now()
		expiredChats := []int{}

		for index, tempChat := range config.TempChats {
			elapsed := now.Sub(tempChat.DateTimeAdded).Minutes()

			if int(elapsed) >= tempChat.TimeAdded {
				expiredChats = append(expiredChats, index)
			}
		}

		sort.Sort(sort.Reverse(sort.IntSlice(expiredChats)))

		for _, index := range expiredChats {
			tempChat := config.TempChats[index]
			err := removeFromWhitelist(config, tempChat.ChatName)
			if err != nil {
				log.Printf("Error removing channel from whitelist: %v", err)
			} else {
				removeFromWhitelist(config, tempChat.ChatName)
				log.Printf("Temporary channel %s removed from whitelist after %d minutes", tempChat.ChatName, tempChat.TimeAdded)

				// Send a private message to the allowed user
				msg := tgbotapi.NewMessage(int64(config.AllowedUser), fmt.Sprintf("Temporary channel %s removed from whitelist after %d minutes.", tempChat.ChatName, tempChat.TimeAdded))
				bot.Send(msg)
			}

			// Remove the temporary chat from the TempChats slice
			config.TempChats = append(config.TempChats[:index], config.TempChats[index+1:]...)

			// Update the JSON file
			writeConfig("test-config.json", *config)
		}
	}
}
*/
