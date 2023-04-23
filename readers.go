package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func readBotToken(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}

	return "", fmt.Errorf("no token found in %s", filename)
}

func saveConfig(filename string, config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filename, data, 0644)
	if err != nil {
		return err
	}

	return nil
}

func downloadAndSavePhoto(bot *tgbotapi.BotAPI, fileID, savePath string) (string, error) {
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", err
	}

	// Create the savePath directory if it doesn't exist
	err = os.MkdirAll(savePath, 0755)
	if err != nil {
		return "", err
	}

	resp, err := http.Get(file.Link(bot.Token))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	photoFilename := filepath.Join(savePath, fileID+".jpg")
	outputFile, err := os.Create(photoFilename)
	if err != nil {
		return "", err
	}
	defer outputFile.Close()

	_, err = io.Copy(outputFile, resp.Body)
	if err != nil {
		return "", err
	}

	return photoFilename, nil
}
