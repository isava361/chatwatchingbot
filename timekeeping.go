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

    _, err := getCurrentTimeForLocation(location)
    if err != nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, "This location is not being available. Please try different town in this time zone")
        bot.Send(msg)
        return
    }

	// Prepare SQL statement to insert the new timezone for the specific chat.
	stmt, err := db.Prepare("INSERT INTO timezones (chatID, location) VALUES (?, ?)")
	if err != nil {
		log.Printf("Error preparing statement: %v", err)
		return
	}
	defer stmt.Close()

	// Execute the statement with the chat ID and location.
	_, err = stmt.Exec(message.Chat.ID, location)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			msg := tgbotapi.NewMessage(message.Chat.ID, "This location has already been added.")
			bot.Send(msg)
		} else {
			log.Printf("Error executing statement: %v", err)
		}
		return
	}
   
    // Query for all current locations.
	rows, err := db.Query("SELECT location FROM timezones WHERE chatID = ?", message.Chat.ID)
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
    location := message.CommandArguments()


    // Prepare SQL statement to remove the timezone for the specific chat.
    stmt, err := db.Prepare("DELETE FROM timezones WHERE chatID = ? AND location = ?")
    if err != nil {
        log.Printf("Error preparing statement: %v", err)
        return
    }
    defer stmt.Close()

    // Execute the statement with the chat ID and location.
    result, err := stmt.Exec(message.Chat.ID, location)
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

    // Message back to the chat depending on whether a row was deleted.
    var msgText string
    if rowsAffected > 0 {
        msgText = fmt.Sprintf("Timezone location '%s' removed successfully!", location)
    } else {
        msgText = fmt.Sprintf("No timezone location found for '%s'.", location)
    }

    msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
    bot.Send(msg)
}

func updateTimeMessage(bot *tgbotapi.BotAPI, chatid int64, db *sql.DB) {
	// Define a struct to hold location, display location, and their current time.
	type locationTime struct {
		Location      string
		DisplayLocation string
		CurrentTime   time.Time
	}

	var locations []locationTime

	// Query for locations and their aliases (if any) associated with the specific chat.
	query := `
	SELECT tz.location, IFNULL(a.alias, tz.location) AS display_location
	FROM timezones tz
	LEFT JOIN alias a ON tz.chatID = a.chatID AND tz.location = a.location
	WHERE tz.chatID = ?`
	
	rows, err := db.Query(query, chatid)
	if err != nil {
		log.Printf("Error querying locations for chat %d: %v", chatid, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var loc, displayLoc string
		if err := rows.Scan(&loc, &displayLoc); err != nil {
			log.Printf("Error scanning row for chat %d: %v", chatid, err)
			return
		}
		currentTime, err := getCurrentTimeForLocation(loc)
		if err != nil {
			log.Printf("Error getting current time for location %s: %v", loc, err)
			continue
		}
		locations = append(locations, locationTime{Location: loc, DisplayLocation: displayLoc, CurrentTime: currentTime})
	}

	if err = rows.Err(); err != nil {
		log.Printf("Error with rows for chat %d: %v", chatid, err)
		return
	}

	// Sort the locations by CurrentTime.
	sort.Slice(locations, func(i, j int) bool {
		return locations[i].CurrentTime.Before(locations[j].CurrentTime)
	})

	// Construct the message text using the sorted locations.
	var messageText string
	for _, loc := range locations {
		messageText += fmt.Sprintf("%s %s\n", loc.DisplayLocation, loc.CurrentTime.Format("15:04"))
	}

    // Check if there is an existing message ID for this chat.
    var messageID int
    err = db.QueryRow("SELECT messageID FROM messagelist WHERE chatID = ?", chatid).Scan(&messageID)

    switch {
    case err == sql.ErrNoRows:
        // If no existing message, send a new one and record its message ID.
        msg := tgbotapi.NewMessage(chatid, messageText)
        sentMsg, err := bot.Send(msg)
        if err != nil {
            log.Printf("Error sending message: %v", err)
            return
        }

        // Insert the new message ID into the database.
        _, err = db.Exec("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", chatid, sentMsg.MessageID)
        if err != nil {
            log.Printf("Error inserting new messageID: %v", err)
        }

    case err == nil:
        // If there is an existing message, edit it.
        edit := tgbotapi.NewEditMessageText(chatid, messageID, messageText)
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
    // List of common prefixes to try
    prefixes := []string{"Europe/", "America/", "Asia/", "Africa/", "Australia/"}

    // Normalize location: replace spaces with underscores and convert to Title case
    locationParts := strings.Split(strings.ToLower(location), " ")
    for i, part := range locationParts {
        locationParts[i] = strings.Title(part)
    }
    normalizedLocation := strings.Join(locationParts, "_")

    var loc *time.Location
    var err error

    // First, try the raw location string in case it's already a full IANA identifier
    loc, err = time.LoadLocation(normalizedLocation)
    if err == nil {
        return time.Now().In(loc), nil
    }

    // If not successful, try with different regional prefixes
    for _, prefix := range prefixes {
        loc, err = time.LoadLocation(prefix + normalizedLocation)
        if err == nil {
            return time.Now().In(loc), nil
        }
    }

    // If none of the combinations worked, return the last error
    log.Printf("Error loading location: %v", err)
    return time.Time{}, err
}

func addOrUpdateAlias(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) {
    // Extract and split the command arguments into location and alias.
    args := strings.SplitN(message.CommandArguments(), "-", 2)
    if len(args) != 2 {
        msg := tgbotapi.NewMessage(message.Chat.ID, "Invalid format. Please use 'Location - Alias'.")
        bot.Send(msg)
        return
    }
    location := strings.TrimSpace(args[0])
    alias := strings.TrimSpace(args[1])

    // Prepare SQL statement to check if the alias already exists for this location.
    checkStmt, err := db.Prepare("SELECT EXISTS(SELECT 1 FROM alias WHERE chatID = ? AND location = ?)")
    if err != nil {
        log.Printf("Error preparing check statement: %v", err)
        return
    }
    defer checkStmt.Close()

    var exists bool
    err = checkStmt.QueryRow(message.Chat.ID, location).Scan(&exists)
    if err != nil {
        log.Printf("Error executing check statement: %v", err)
        return
    }

    // Based on existence, either insert or update the alias.
    var sqlStmt string
    if exists {
        // Update existing alias
        sqlStmt = "UPDATE alias SET alias = ? WHERE chatID = ? AND location = ?"
    } else {
        // Insert new alias
        sqlStmt = "INSERT INTO alias (chatID, location, alias) VALUES (?, ?, ?)"
    }

    stmt, err := db.Prepare(sqlStmt)
    if err != nil {
        log.Printf("Error preparing statement: %v", err)
        return
    }
    defer stmt.Close()

    // Execute the statement.
    if exists {
        _, err = stmt.Exec(alias, message.Chat.ID, location)
    } else {
        _, err = stmt.Exec(message.Chat.ID, location, alias)
    }

    if err != nil {
        log.Printf("Error executing statement: %v", err)
        return
    }

    // Confirmation message back to the chat.
    confirmationMsg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Alias '%s' set for location '%s'", alias, location))
    bot.Send(confirmationMsg)
}

func getAllActiveChatIDs(db *sql.DB) ([]int64, error) {
    var chatIDs []int64

    // Query to select all distinct chatIDs from the messagelist table
    rows, err := db.Query("SELECT DISTINCT chatID FROM timezones")
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    // Iterate through the rows and append each chatID to the slice
    for rows.Next() {
        var chatID int64
        if err := rows.Scan(&chatID); err != nil {
            return nil, err
        }
        chatIDs = append(chatIDs, chatID)
    }

    // Check for errors encountered during iteration
    if err = rows.Err(); err != nil {
        return nil, err
    }

    return chatIDs, nil
}

func resetMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) {
	chatID := message.Chat.ID
	// Prepare SQL statement to remove the timezone for the specific chat.
	stmt, err := db.Prepare("DELETE FROM messagelist WHERE chatID = ?")
	if err != nil {
		log.Printf("Error preparing statement: %v", err)
		return
	}
	defer stmt.Close()

	// Execute the statement with the chat ID and location.
	_, err = stmt.Exec(chatID)
	if err != nil {
		log.Printf("Error executing statement: %v", err)
		return
	}
	updateTimeMessage(bot, chatID, db)
}

func isTimeBetween2AMAnd7AM(t time.Time) bool {
    hour := t.Hour()
    return hour >= 2 && hour <= 7
}

func isTimeBetween19And8(t time.Time) bool {
    hour := t.Hour()
    return hour >= 19 || hour <= 8
}