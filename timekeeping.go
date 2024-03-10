package main

import (
	"database/sql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"
	"fmt"
	"log"
	"strings"
	"time"
    "sort"
    timezone "github.com/tkuchiki/go-timezone"

)

func timeAdd(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *sql.DB) {
	// Extract the command arguments which is the location.
	location := message.CommandArguments()

    _, err := getCurrentTimeForLocation(location)
    if err != nil {
        msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Invalid location: %s. Please provide a valid timezone location.", location))
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
            // Send an error message to the user
            msg := tgbotapi.NewMessage(chatid, fmt.Sprintf("Invalid location: %s. Please update the location using /addlocation.", loc))
            bot.Send(msg)
            continue
        }
        locations = append(locations, locationTime{Location: loc, DisplayLocation: displayLoc, CurrentTime: currentTime})
    }

    
 /*   // Debugging: print comparison results during sorting with UTC conversion
    sort.Slice(locations, func(i, j int) bool {
        // Extract the hour and minute for both locations, considering their timezone.
        hourI, minI, _ := locations[i].CurrentTime.Clock()
        hourJ, minJ, _ := locations[j].CurrentTime.Clock()
    
        // First, compare the hours.
        if hourI != hourJ {
            return hourI < hourJ
        }
    
        // If the hours are equal, compare the minutes.
        return minI < minJ
    })
*/

    sort.Slice(locations, func(i, j int) bool {
        // Convert CurrentTime to a "local hour" extending beyond 24 for sorting.
        // This is a simplistic approach and might need adjustment for your exact use case.
        localHourI := locations[i].CurrentTime.Hour() + locations[i].CurrentTime.Day()*24
        localHourJ := locations[j].CurrentTime.Hour() + locations[j].CurrentTime.Day()*24

        // If the "local hours" are equal, further compare minutes for precise ordering.
        if localHourI == localHourJ {
            return locations[i].CurrentTime.Minute() < locations[j].CurrentTime.Minute()
        }
        return localHourI < localHourJ
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

    }
}


func getCurrentTimeForLocation(location string) (time.Time, error) {
    // Normalize location: replace spaces with underscores and convert to Title case
    locationParts := strings.Split(strings.ToLower(location), " ")
    for i, part := range locationParts {
        locationParts[i] = strings.Title(part)
    }
    normalizedLocation := strings.Join(locationParts, "_")

    // Use the go-timezone package to load the location
    loc, err := timezone.GetTimezones(normalizedLocation)
    if err != nil || len(loc) == 0 {
        log.Printf("Error loading location: %v", err)
        return time.Time{}, fmt.Errorf("invalid location: %s", location)
    }

    // Return the current time in the first matched location
    return time.Now().In(loc[0]), nil
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

func isTimeBetween(t time.Time, hour1 int, hour2 int) bool {
    hour := t.Hour()
    return hour >= hour1 && hour <= hour2
}

func isTimeBetween19And8(t time.Time) bool {
    hour := t.Hour()
    return hour >= 19 || hour <= 8
}