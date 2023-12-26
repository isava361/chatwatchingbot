package main

import (
	"fmt"
	"image"
	_ "image/jpeg" // Support for JPEG format
	_ "image/png"  // Support for PNG format
	"io"
	"net/http"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// decodeQRCode downloads the photo, decodes the QR code, and deletes the photo.
func decodeQRCode(bot *tgbotapi.BotAPI, message *tgbotapi.Message) (string, error) {
	if message.Photo == nil || len(*message.Photo) == 0 {
		return "", fmt.Errorf("no photo in the message")
	}

	// Get the highest resolution photo
	photos := *message.Photo
	fileID := photos[len(photos)-1].FileID

	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		return "", err
	}

	// Download the photo to the /temp directory
	tempFilePath, err := downloadPhoto(fileURL)
	if err != nil {
		return "", err
	}
	defer os.Remove(tempFilePath) // Ensure the file is deleted after processing

	// Open and decode the QR code from the photo
	file, err := os.Open(tempFilePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	return decodeQRFromReader(file)
}

// downloadPhoto downloads a photo from the given URL to the /temp directory.
func downloadPhoto(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tempFile, err := os.CreateTemp("", "photo_*.jpg")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", err
	}

	return tempFile.Name(), nil
}

// decodeQRFromReader decodes a QR code from an io.Reader
func decodeQRFromReader(reader io.Reader) (string, error) {
	img, _, err := image.Decode(reader)
	if err != nil {
		return "", err
	}

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}

	qrReader := qrcode.NewQRCodeReader()
	result, err := qrReader.Decode(bmp, nil)
	if err != nil {
		return "", err
	}

	return result.String(), nil
}
