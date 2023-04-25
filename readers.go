package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"io"
	"net/http"
	"path/filepath"
	"bytes"
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

func (fw *FileWriter) Get(key string) (string, error) { }
func (fw *FileWriter) Put(config Config) error {
	file, err := os.Create(fw.FileName)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := new(bytes.Buffer)
	encoder := json.NewEncoder(buf)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(config)
	if err != nil {
		return err
	}

	_, err = file.Write(buf.Bytes())
	if err != nil {
		return err
	}

	return nil
}
  

func downloadAndSaveFile(bot *tgbotapi.BotAPI, fileID, savePath, extension string) (string, error) {
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

	filename := filepath.Join(savePath, fileID + extension)
	outputFile, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer outputFile.Close()

	_, err = io.Copy(outputFile, resp.Body)
	if err != nil {
		return "", err
	}

	return filename, nil
}
