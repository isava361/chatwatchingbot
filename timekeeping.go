package main

import (
	"database/sql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"
	"fmt"
	"log"
	"strings"
	"time"
)

func timeAdd(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) {
    // Extract the command arguments which is the location.
    location := message.CommandArguments()

    // Prepare SQL statement to insert the new timezone.
    stmt, err := db.Prepare("INSERT INTO timezones (chatID, location) VALUES ($1, $2)")
    if err != nil {
        log.Printf("Error preparing statement: %v", err)
        return
    }
    defer stmt.Close()

    // Execute the statement with the chat ID and location.
    _, err = stmt.Exec(message.Chat.ID, location)
    if err != nil {
        log.Printf("Error executing statement: %v", err)
        return
    }

    // Query for all current locations.
    rows, err := db.Query("SELECT location FROM timezones")
    if err != nil {
        log.Printf("Error querying locations: %v", err)
        return
    }
    defer rows.Close()

    var locations []string
    for rows.Next() {
        var loc string
        if err := rows.Scan(&loc); err != nil {
            log.Printf("Error scanning row: %v", err)
            return
        }
        locations = append(locations, loc)
    }

    if err = rows.Err(); err != nil {
        log.Printf("Error with rows: %v", err)
        return
    }

    // Send a confirmation message back to the chat.
    confirmationMsg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Timezone location '%s' added successfully!", location))
    _, err = bot.Send(confirmationMsg)
    if err != nil {
        log.Printf("Error sending confirmation message: %v", err)
        return
    }

    // Send the current locations message.
    locationsMsg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Current locations are: %s", strings.Join(locations, ", ")))
    _, err = bot.Send(locationsMsg)
    if err != nil {
        log.Printf("Error sending locations message: %v", err)
        return
    }
}

func timeRemove(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) {
    // Convert location to lowercase
    location := strings.ToLower(message.CommandArguments())

    // Prepare SQL statement to remove the timezone.
    stmt, err := db.Prepare("DELETE FROM timezones WHERE location = $1")
    if err != nil {
        log.Printf("Error preparing statement: %v", err)
        return
    }
    defer stmt.Close()

    // Execute the statement with the location.
    result, err := stmt.Exec(location)
    if err != nil {
        log.Printf("Error executing statement: %v", err)
        return
    }

    // Check if any row was affected.
    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Printf("Error getting rows affected: %v", err)
        return
    }

    // Send a message back to the chat depending on whether a row was deleted.
    var msgText string
    if rowsAffected > 0 {
        msgText = fmt.Sprintf("Timezone location '%s' removed successfully!", location)
    } else {
        msgText = fmt.Sprintf("No timezone location found for '%s'.", location)
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
    _, err = bot.Send(msg)
    if err != nil {
        log.Printf("Error sending message: %v", err)
        return
    }
}

func updateTimeMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) {
    // Query for all locations.
    rows, err := db.Query("SELECT location FROM timezones")
    if err != nil {
        log.Printf("Error querying locations: %v", err)
        return
    }
    defer rows.Close()

    var messageText string
    var locations []string
	for rows.Next() {
		var loc string
		if err := rows.Scan(&loc); err != nil {
			log.Printf("Error scanning row: %v", err)
			return
		}
		currentTime, err := getCurrentTimeForLocation(loc)
		if err != nil {
			log.Printf("Error getting current time for location %s: %v", loc, err)
			// Decide how you want to handle the error. For example, you might continue to the next record.
			continue
		}
		messageText += fmt.Sprintf("%s %s\n", loc, currentTime.Format("15:04"))
		locations = append(locations, loc)
	}
	

    if err = rows.Err(); err != nil {
        log.Printf("Error with rows: %v", err)
        return
    }

    // Check if there is an existing message ID for this chat.
    var messageID int
    err = db.QueryRow("SELECT messageID FROM messagelist WHERE chatID = ?", message.Chat.ID).Scan(&messageID)

    switch {
    case err == sql.ErrNoRows:
        // If no existing message, send a new one and record its message ID.
        msg := tgbotapi.NewMessage(message.Chat.ID, messageText)
        sentMsg, err := bot.Send(msg)
        if err != nil {
            log.Printf("Error sending message: %v", err)
            return
        }

        // Insert the new message ID into the database.
        _, err = db.Exec("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", message.Chat.ID, sentMsg.MessageID)
        if err != nil {
            log.Printf("Error inserting new messageID: %v", err)
        }

    case err == nil:
        // If there is an existing message, edit it.
        edit := tgbotapi.NewEditMessageText(message.Chat.ID, messageID, messageText)
        _, err = bot.Send(edit)
        if err != nil {
            log.Printf("Error editing message: %v", err)
        }

    default:
        // Handle other potential errors.
        log.Printf("Error querying for existing messageID: %v", err)
    }
}

func getCurrentTimeForLocation(location string) (time.Time, error) {
    // Load location using the IANA Time Zone database
    loc, err := time.LoadLocation(location)
    if err != nil {
        log.Printf("Error loading location: %v", err)
        return time.Time{}, err // return zero value of time.Time in case of error
    }

    // Get current time in the specified location
    return time.Now().In(loc), nil
}