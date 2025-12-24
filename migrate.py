import json
import sqlite3

# Connect to the SQLite database
conn = sqlite3.connect('mydb.db')
cursor = conn.cursor()

# Read the JSON file
with open('./config/config.json') as file:
    data = json.load(file)

# Iterate over the chat triggers
for chat_id, triggers in data['chat_triggers'].items():
    for trigger in triggers:
        search_phrase = trigger['searchPhrase']
        response = trigger.get('response', None)
        file_type = trigger.get('fileType', None)
        file_id = trigger.get('fileID', None)
        file_name = trigger.get('filename', None)
        is_global = False  # Assuming chat-specific triggers are not global

        # Insert the trigger into the database
        cursor.execute('''
            INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, is_global)
            VALUES (?, ?, ?, ?, ?, ?, ?)
        ''', (chat_id, search_phrase, response, file_type, file_id, file_name, is_global))

# Iterate over the global responses
for response in data['myResponses']:
    search_phrase = response['searchPhrase']
    response_text = response.get('response', None)
    file_type = response.get('fileType', None)
    file_id = response.get('fileID', None)
    file_name = response.get('filename', None)
    is_global = True  # Global responses are marked as global

    # Insert the global response into the database
    cursor.execute('''
        INSERT INTO triggers (search_phrase, response, file_type, file_id, file_name, is_global)
        VALUES (?, ?, ?, ?, ?, ?)
    ''', (search_phrase, response_text, file_type, file_id, file_name, is_global))

# Commit the changes and close the connection
conn.commit()
conn.close()
