package main

import (
	"errors"
	"project/services/messages/messagestypes"
	"sync"

	"github.com/google/uuid"
)

type Chat interface {
	Create([]userID) (chatID, error)
	Write(chatid chatID, userid userID, msgtype messagestypes.MessageContentType, message []byte, timestamp int64) error
	GetUsers(chatID) []userID
	GetUserChats(userID) []chatID
	GetMessagesAfter(chatid chatID, timestamp int64) ([]message, error)
}

type chats struct {
	chatrooms map[chatID]*chatroom
	sync.Mutex
}

type chatroom struct {
	users    []userID
	messages []message

	sync.Mutex
}

func (c *chats) Create(users []userID) (chatID, error) {
	uid, err := uuid.NewRandom()
	if err != nil {
		return "", nil
	}
	c.Lock()
	defer c.Unlock()

	chuid := chatID(uid.String())
	c.chatrooms[chuid] = &chatroom{users: users}
	return chuid, nil
}

func (c *chats) GetUsers(chatid chatID) []userID {
	c.Lock()
	defer c.Unlock()

	if cr, ok := c.chatrooms[chatid]; ok {
		cr.Lock()
		defer cr.Unlock()

		f := make([]userID, len(cr.users))
		copy(f, cr.users)

		return f
	} else {
		return nil
	}
}
func (c *chats) GetUserChats(uid userID) []chatID {
	c.Lock()
	defer c.Unlock()
	uc := make([]chatID, 0, 1)
	for roomid, room := range c.chatrooms {
		room.Lock()
		for _, usuid := range room.users {
			if usuid == uid {
				uc = append(uc, roomid)
			}
		}
		room.Unlock()
	}
	return uc
}
func (c *chats) GetMessagesAfter(chatid chatID, timestamp int64) ([]message, error) {
	c.Lock()

	if cr, ok := c.chatrooms[chatid]; ok {
		c.Unlock()

		cr.Lock()
		defer cr.Unlock()
		for i := 0; i < len(cr.messages); i++ {
			if cr.messages[i].Timestamp > timestamp {
				msgs := make([]message, len(cr.messages)-i)
				msgs = append(msgs, cr.messages[i:]...)
				return msgs, nil
			}
		}
		return make([]message, 0), nil
	} else {
		c.Unlock()
		return nil, errors.New("unknown chatid")
	}
}

func (c *chats) Write(chatid chatID, userid userID, msgtype messagestypes.MessageContentType, msg []byte, timestamp int64) error {
	c.Lock()
	if cr, ok := c.chatrooms[chatid]; ok {
		c.Unlock()

		cr.Lock()
		cr.messages = append(cr.messages, message{UserID: userid, ChatID: chatid, MessageType: msgtype, Timestamp: timestamp, Message: msg})
		cr.Unlock()
		return nil
	} else {
		c.Unlock()
		return errors.New("unknown chatid")
	}
}
