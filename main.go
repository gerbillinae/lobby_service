package main

import (
	"bufio"
	"errors"
	"flag"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

// temporary protection
const (
	MAX_ROOMS    = 500
	MAX_USERS    = 20
	MAX_INFO_LEN = 1024
)

const (
	EVENT_DISCONNECTED = "disconnected"
	EVENT_USER_ADDED   = "user_added"
	EVENT_USER_RENAMED = "user_renamed"
	EVENT_COMPLETE     = "complete"
)

type User struct {
	token  string `json:"-"`
	Id     int    `json:"id"`
	Name   string `json:"name"`
	events chan any
}

func init_user(id int, token string) *User {
	return &User{token: token, Id: id, Name: "", events: make(chan any)}
}

// Room state
const (
	ROOM_OPEN = iota
	ROOM_LOCKED
	ROOM_COMPLETE
)

type Room struct {
	next_user_id   int        `json:"-"`
	mutex          sync.Mutex `json:"-"`
	state          int        `json:"-"` // Room state
	Users          []*User    `json:"users"`
	CreationInfo   string     `json:"creation_info"`
	CompletionInfo string     `json:"completion_info,omitempty"`

	CreatedAt  time.Time   `json:"-"`
	TimeToLive time.Time   `json:"-"`
	TTLTimer   *time.Timer `json:"-"`
}

func delete_room(room_id string) {
	log.Printf("delete_room %s", room_id)
	RoomsMutex.Lock()
	room := Rooms[room_id]
	if room != nil {
		delete(Rooms, room_id)
		RoomsMutex.Unlock()
		room.Close()
		return
	}
	RoomsMutex.Unlock()
}

func init_room(creationInfo string) (string, *Room, error) {
	RoomsMutex.Lock()
	defer RoomsMutex.Unlock()

	if len(creationInfo) >= MAX_INFO_LEN {
		return "", nil, errors.New("Max info length exceeded")
	}

	if len(Rooms) >= MAX_ROOMS {
		return "", nil, errors.New("Max room limit reached")
	}

	room_id := new_room_id(4)
	for Rooms[room_id] != nil {
		room_id = new_room_id(4)
	}

	now := time.Now()
	ttl := now.Add(time.Minute * 5)
	timer := time.AfterFunc(time.Minute*5, func() {
		delete_room(room_id)
	})

	room := &Room{CreationInfo: creationInfo, state: ROOM_OPEN, CreatedAt: now, TimeToLive: ttl, TTLTimer: timer}

	Rooms[room_id] = room
	return room_id, room, nil
}

func (room *Room) Close() {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	ntf := struct {
		MessageType string `json:"message_type"`
		Reason      string `json:"reason"`
	}{
		MessageType: EVENT_DISCONNECTED,
		Reason:      "closed",
	}

	for _, user := range room.Users {
		select {
		case user.events <- ntf:
		default:
		}
	}
}

func (room *Room) Complete(room_id string, completionInfo string) error {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if room.CompletionInfo != "" {
		return errors.New("Room already complete")
	}

	if len(completionInfo) >= MAX_INFO_LEN {
		return errors.New("Max info length exceeded")
	}

	if !(room.state == ROOM_OPEN || room.state == ROOM_LOCKED) {
		return errors.New("Expected room to be open or locked before completing")
	}

	room.state = ROOM_COMPLETE
	room.CompletionInfo = completionInfo

	room.TTLTimer.Stop()
	now := time.Now()
	room.TimeToLive = now.Add(time.Second * 10)
	room.TTLTimer = time.AfterFunc(time.Second*10, func() {
		delete_room(room_id)
	})

	ntf := struct {
		MessageType    string `json:"message_type"`
		CompletionInfo string `json:"completion_info"`
	}{
		MessageType:    EVENT_COMPLETE,
		CompletionInfo: room.CompletionInfo,
	}

	for _, user := range room.Users {
		select {
		case user.events <- ntf:
		default:
		}
	}
	return nil
}

func (room *Room) Join(name string) (int, string, error) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if len(room.Users) >= MAX_USERS {
		return -1, "", errors.New("Max user limit reached")
	}

	if room.state != ROOM_OPEN {
		return -1, "", errors.New("Room cannot be joined")
	}

	id, err := uuid.NewV7()
	if err != nil {
		return -1, "", errors.New("Failed to create token")
	}

	user_id := id.String()

	public_id := room.next_user_id
	room.next_user_id += 1

	user := init_user(public_id, user_id)
	user.Name = name
	room.Users = append(room.Users, user)

	ntf := struct {
		MessageType string `json:"message_type"`
		Id          int    `json:"id"`
		Name        string `json:"name"`
	}{
		MessageType: EVENT_USER_ADDED,
		Id:          public_id,
		Name:        name,
	}

	for _, user := range room.Users {
		select {
		case user.events <- ntf:
		default:
		}
	}

	return public_id, user_id, nil
}

func (room *Room) GetPublicUserId(token string) (int, error) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	for _, user := range room.Users {
		if user.token == token {
			return user.Id, nil
		}
	}

	return 0, errors.New("Token not found")
}

func (room *Room) GetChannel(token string) (chan any, error) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	for _, user := range room.Users {
		if user.token == token {
			ntf := struct {
				MessageType string `json:"message_type"`
				Reason      string `json:"reason"`
			}{
				MessageType: EVENT_DISCONNECTED,
				Reason:      "replaced",
			}
			select {
			case user.events <- ntf:
			default:
			}
			return user.events, nil
		}
	}

	return nil, errors.New("Token not found")
}

func (room *Room) Rename(token string, name string) error {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if room.state != ROOM_OPEN && room.state != ROOM_LOCKED {
		return errors.New("Rename not allowed")
	}

	found := false
	var public_id int = -1

	for _, user := range room.Users {
		if user.token == token {
			if user.Name == name {
				return nil
			}
			found = true
			public_id = user.Id
			user.Name = name
		}
	}

	if !found {
		return errors.New("token not found")
	}

	ntf := struct {
		MessageType string `json:"message_type"`
		Id          int    `json:"id"`
		Name        string `json:"name"`
	}{
		MessageType: EVENT_USER_RENAMED,
		Id:          public_id,
		Name:        name,
	}

	for _, user := range room.Users {
		select {
		case user.events <- ntf:
		default:
		}
	}

	return nil
}

var RoomsMutex = sync.Mutex{}
var Rooms = make(map[string]*Room)

func get_room(room string) *Room {
	RoomsMutex.Lock()
	defer RoomsMutex.Unlock()
	return Rooms[room]
}

func new_room_id(length int) string {
	const charset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	result := make([]byte, length)
	for i := range length {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}

func handle_create(c *gin.Context) {
	var requestData map[string]string
	if err := c.BindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	creation_info := requestData["creation_info"]
	if creation_info == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing creation_info"})
		return
	}

	name := requestData["name"]

	room_id, room, err := init_room(creation_info)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id, token, err := room.Join(name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	Rooms[room_id] = room

	c.JSON(http.StatusOK, gin.H{"status": "success", "room": room_id, "user_id": id, "token": token})
}
func handle_join(c *gin.Context) {
	var requestData map[string]string
	if err := c.BindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	room_id := requestData["room"]
	if room_id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`room` is mandatory"})
		return
	}

	name := requestData["name"]

	func() {
		RoomsMutex.Lock()
		defer RoomsMutex.Unlock()
		room := Rooms[room_id]

		if room == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Room not found"})
			return
		}

		id, token, err := room.Join(name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success", "id": id, "token": token, "info": room.CreationInfo})
	}()
}

func handle_name(c *gin.Context) {
	var requestData map[string]string
	if err := c.BindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	room_id := requestData["room"]
	if room_id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`room` is mandatory"})
		return
	}

	name := requestData["name"]
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`name` is mandatory"})
		return
	}

	token := requestData["token"]
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`token` is mandatory"})
		return
	}

	func() {
		RoomsMutex.Lock()
		defer RoomsMutex.Unlock()
		room := Rooms[room_id]

		if room == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Room not found"})
			return
		}

		err := room.Rename(token, name)

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}()
}

func handle_complete(c *gin.Context) {
	var requestData map[string]string
	if err := c.BindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	room_id := requestData["room"]
	if room_id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`room` is mandatory"})
		return
	}

	completion_info := requestData["completion_info"]
	if completion_info == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`completionInfo` is mandatory"})
		return
	}

	token := requestData["token"]
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`token` is mandatory"})
		return
	}

	room := get_room(room_id)

	if room == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Room not found"})
		return
	}

	id, err := room.GetPublicUserId(token)

	if id != 0 || err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Permission denied"})
		return
	}

	err = room.Complete(room_id, completion_info)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func handle_info(c *gin.Context) {
	room_id := c.Query("room")
	if room_id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`room` is mandatory"})
		return
	}

	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`token` is mandatory"})
		return
	}

	room := get_room(room_id)

	if room == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Room not found"})
		return
	}

	if _, err := room.GetPublicUserId(token); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Permission Denied"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "info": room})

}

func handle_events(c *gin.Context) {
	log.Println("EVENTS")
	room_id := c.Query("room")
	if room_id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`room` is mandatory"})
		return
	}

	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "`token` is mandatory"})
		return
	}

	log.Println("EVENTS GET ROOM")
	room := get_room(room_id)

	if room == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "room not found"})
		return
	}

	if _, err := room.GetPublicUserId(token); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Permission Denied"})
		return
	}

	log.Println("EVENTS GET CHAN")
	user_chan, err := room.GetChannel(token)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Flush()
	log.Println("EVENTS FLUSH HEADERS")

	c.Stream(func(w io.Writer) bool {

		if msg, ok := <-user_chan; ok {

			v := reflect.ValueOf(msg)
			message_type_field := v.FieldByName("MessageType")

			if !message_type_field.IsValid() || message_type_field.Kind() != reflect.String {
				panic("Expected message_type on all notifications!")
			}

			msgType := message_type_field.String()
			log.Printf("Sending notification of type: %s", msgType)

			c.SSEvent(msgType, msg)

			return msgType != EVENT_DISCONNECTED && msgType != EVENT_COMPLETE
		}
		return false
	})
}

func main() {
	port := flag.String("port", "8080", "Which port to listen on.")
	flag.Parse()

	loadEnvFile("package.env")
	prefix := os.Getenv("PATH_PREFIX")
	version := os.Getenv("IMAGE_VERSION")

	r := gin.Default()

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "PAGE_NOT_FOUND",
			"message": "The requested URL was not found on the server.",
			"url":     c.Request.URL.String(),
		})
	})

	r.GET(prefix+"/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": version})
	})

	r.POST(prefix+"/create", handle_create)

	r.POST(prefix+"/join", handle_join)

	r.POST(prefix+"/name", handle_name)

	// Lock a room, preventing new users from joining
	r.POST(prefix+"/complete", handle_complete)

	r.GET(prefix+"/info", handle_info)

	r.GET(prefix+"/events", handle_events)

	r.Run(":" + *port)
}

func loadEnvFile(filePath string) error {
	// Open the .env file
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read the file line by line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split the line into key and value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // Invalid line, skip it
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Set the environment variable
		os.Setenv(key, value)
	}

	return scanner.Err()
}
