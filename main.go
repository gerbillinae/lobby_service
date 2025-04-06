package main

import (
	"errors"
	"flag"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"log"
	"math/rand"
	"net/http"
	"reflect"
	"sync"
	"time"
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
	next_user_id   int         `json:"-"`
	mutex          sync.Mutex  `json:"-"`
	state          int         `json:"-"` // Room state
	Users          []*User     `json:"users"`
	CreationInfo   string      `json:"creation_info"`
	CompletionInfo string      `json:"completion_info,omitempty"`
	CreatedAt      time.Time   `json:"-"`
	TTLTimer       *time.Timer `json:"-"`
}

func delete_room(room_id string) {
	RoomsMutex.Lock()
	defer RoomsMutex.Unlock()
	delete(Rooms, room_id)
}

func init_room(creationInfo string) (string, *Room) {
	RoomsMutex.Lock()
	defer RoomsMutex.Unlock()

	room_id := new_room_id(4)
	for Rooms[room_id] != nil {
		room_id = new_room_id(4)
	}

	now := time.Now()
	room := &Room{CreationInfo: creationInfo, state: ROOM_OPEN, CreatedAt: now, TTLTimer: time.AfterFunc(time.Minute*5, func() {
		delete_room(room_id)
	})}

	Rooms[room_id] = room
	return room_id, room
}

func (room *Room) Complete(completionInfo string) error {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if room.CompletionInfo != "" {
		return errors.New("Room already complete")
	}

	room.state = ROOM_COMPLETE
	room.CompletionInfo = completionInfo

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

func main() {
	port := flag.String("port", "8080", "Which port to listen on.")

	r := gin.Default()
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	r.POST("/create", func(c *gin.Context) {
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

		room_id, room := init_room(creation_info)
		id, token, err := room.Join(name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		Rooms[room_id] = room

		c.JSON(http.StatusOK, gin.H{"status": "success", "room": room_id, "user_id": id, "token": token})
	})

	r.POST("/join", func(c *gin.Context) {
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
	})

	r.POST("/name", func(c *gin.Context) {
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
	})

	// Lock a room, preventing new users from joining
	r.POST("/complete", func(c *gin.Context) {
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

		err = room.Complete(completion_info)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success"})
	})

	r.GET("/info", func(c *gin.Context) {
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

	})

	r.GET("/events", func(c *gin.Context) {
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

	})

	r.Run(":" + *port)
}
