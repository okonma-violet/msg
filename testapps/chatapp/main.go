package main

import (
	"context"
	"encoding/json"
	"errors"

	"project/services/messages/messagestypes"
	"strconv"
	"sync"

	"github.com/big-larry/suckutils"
	"github.com/google/uuid"

	"github.com/okonma-violet/msg/appservice"
	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"
)

// read this from configfile
type config struct {
	ClickhouseAddr  []string
	ClickhouseTable string
}

// type appUID uuid.UUID
type userID string

// type chatID bson.ObjectId
type chatID string

// your shit here
type service struct {
	users       map[userID]*user
	connections map[protocol.ConnUID]*user

	chats Chat

	appserv appservice.Sender

	l logger.Logger
	sync.Mutex
}

type user struct {
	connUIDS []protocol.ConnUID

	sync.Mutex
}

func (s *service) newUser() userID {
	uid := uuid.New()

	s.Lock()
	defer s.Unlock()
	struid := userID(uid.String())
	s.users[struid] = &user{connUIDS: make([]protocol.ConnUID, 0, 1)}
	return struid
}

func (s *service) addConnUID(userid userID, connuid protocol.ConnUID) error {
	s.Lock()
	defer s.Unlock()

	if u, ok := s.users[userid]; ok {
		u.Lock()
		u.connUIDS = append(u.connUIDS, connuid)
		u.Unlock()
		s.connections[connuid] = u
		return nil
	} else {
		return errors.New("unknown userid")
	}
}

func (s *service) deleteConnUID(connuid protocol.ConnUID) error {
	s.Lock()
	defer s.Unlock()

	u, ok := s.connections[connuid]
	if !ok {
		return errors.New("unknown connuid")
	}
	delete(s.connections, connuid)

	u.Lock()
	defer u.Unlock()

	for i := 0; i < len(u.connUIDS); i++ {
		if u.connUIDS[i] == connuid {
			u.connUIDS = u.connUIDS[:i+copy(u.connUIDS[i:], u.connUIDS[i+1:])]
			return nil
		}
	}
	return nil
}

type message struct {
	UserID      userID                           `json:"appuid"`
	ChatID      chatID                           `json:"chatid"`
	MessageType messagestypes.MessageContentType `json:"msgtype"`
	Timestamp   int64                            `json:"timestamp"`
	Message     []byte                           `json:"message,omitempty"`
}

const thisServiceName appservice.ServiceName = "app.chat"

func (c *config) CreateHandler(ctx context.Context, l logger.Logger, appserv appservice.Sender, pubs_getter appservice.Publishers_getter) (appservice.Handler, error) {
	users := make(map[userID]*user)
	users["123"] = &user{
		connUIDS: make([]protocol.ConnUID, 0),
	}
	users["124"] = &user{
		connUIDS: make([]protocol.ConnUID, 0),
	}
	ch := &chats{chatrooms: make(map[chatID]*chatroom)}
	if cid, err := ch.Create([]userID{"123", "124"}); err != nil {
		panic(err)
	} else {
		l.Info("hardcode-created chatID", string(cid))
		ch.chatrooms["1234"] = ch.chatrooms[cid]
	}
	return &service{
		users:       users,
		chats:       ch,
		connections: make(map[protocol.ConnUID]*user),
		appserv:     appserv,
		l:           l,
	}, nil
}

type headers struct {
	UserID     userID `json:"appuid"`
	ChatID     chatID `json:"chatid"`
	LastUpdate int64  `json:"timestamp"` // ??????????????????????????переименовать
}

func (s *service) Handle(msg *protocol.AppMessage) error {
	s.l.Debug("Handle", suckutils.Concat("recieved message from ", suckutils.Itoa(uint32(msg.ConnectionUID)), ", messagetype: ", msg.Type.String()))
	hdrs := headers{}
	if len(msg.Headers) > 0 {
		err := json.Unmarshal(msg.Headers, &hdrs)
		if err != nil {
			s.l.Error("Unmarshal headers", err)
			return nil
		}
	}
	switch msg.Type {
	case protocol.TypeRegistration:
		uid := s.newUser()
		if err := s.addConnUID(uid, msg.ConnectionUID); err != nil {
			s.l.Error("Handle/addConnUID", err)
			return nil
		}
		respm, err := protocol.EncodeAppMessage(protocol.TypeRegistration, msg.ConnectionUID, msg.Timestamp, nil, []byte(uid))
		if err != nil {
			s.l.Error("Handle/EncodeAppMessage", err)
			return nil
		}
		s.l.Debug("Handle/Registration", suckutils.Concat("registrated new user, appuid: ", string(uid), ", connuid: ", strconv.Itoa(int(msg.ConnectionUID))))
		s.appserv.Send(respm)
		return nil
	case protocol.TypeConnect:
		if err := s.addConnUID(hdrs.UserID, msg.ConnectionUID); err != nil {
			s.l.Error("Handle/addConnUID", err)
			return nil
		}
		userchatids := s.chats.GetUserChats(hdrs.UserID)
		for i := 0; i < len(userchatids); i++ {
			if newmsgs, err := s.chats.GetMessagesAfter(userchatids[i], hdrs.LastUpdate); err != nil {
				s.l.Error("Handle/GetMessagesAfter", err)
				continue
			} else if len(newmsgs) != 0 {
				body, err := json.Marshal(newmsgs)
				if err != nil {
					s.l.Error("Handle/Marshal", err)
					continue
				}
				updmsg, err := protocol.EncodeAppMessage(protocol.TypeText, msg.ConnectionUID, msg.Timestamp, nil, body)
				if err != nil {
					s.l.Error("Handle/EncodeAppMessage", err)
					return nil
				}
				s.appserv.Send(updmsg)
			}
		}
		s.l.Debug("Handle/Connect", suckutils.Concat("user connected, appuid: ", string(hdrs.UserID), ", chats updates was sent"))
		return nil
	case protocol.TypeCreate:
		withuser := userID(msg.Body)
		newchatid, err := s.chats.Create([]userID{hdrs.UserID, withuser})
		if err != nil {
			s.l.Error("Handle/chats.Create", err)
			return nil
		}
		crtmsg, err := protocol.EncodeAppMessage(protocol.TypeCreate, msg.ConnectionUID, msg.Timestamp, nil, []byte(newchatid))
		if err != nil {
			s.l.Error("Handle/EncodeAppMessage", err)
			return nil
		}
		s.appserv.Send(crtmsg)
		s.l.Debug("Handle/Create", suckutils.Concat("chat created, users: ", string(hdrs.UserID), ", ", string(withuser), ", chatid: ", string(newchatid)))
	case protocol.TypeText:
		// TODO: чекать коннюид по юзерайди, а то сейчас любой от любого лица писать может
		usrs := s.chats.GetUsers(hdrs.ChatID)
		if len(usrs) != 0 {
			if err := s.chats.Write(hdrs.ChatID, hdrs.UserID, messagestypes.Text, msg.Body, msg.Timestamp); err != nil {
				s.l.Error("Handle/chats/Write", err)
				return nil
			}

			for i := 0; i < len(usrs); i++ {
				s.Lock()
				if u, ok := s.users[usrs[i]]; ok {
					u.Lock()
					for _, cuid := range u.connUIDS {
						if m, err := protocol.EncodeAppMessage(protocol.TypeText, cuid, msg.Timestamp, msg.Headers, msg.Body); err != nil {
							s.l.Error("Handle/EncodeAppMessage", err)
							continue
						} else {
							s.appserv.Send(m)
						}
					}
					u.Unlock()
				} else {
					s.l.Error("Handle", errors.New("unknown userid in chatroom"))
				}
				s.Unlock()
			}
		} else {
			s.l.Debug("Handle/GetUsers", "no chatroom with such chatid")
		}
		s.l.Debug("Handle/Text", suckutils.Concat("user ", string(hdrs.UserID), " writed to chat ", string(hdrs.ChatID), " this: ", string(msg.Body)))
	case protocol.TypeDisconnection:
		s.deleteConnUID(msg.ConnectionUID)
		s.l.Debug("Handle/Disconnect", suckutils.ConcatTwo(strconv.Itoa(int(msg.ConnectionUID)), " disconnected"))
	default:
		s.l.Error("Handle", errors.New("unknown messagetype"))
	}

	return nil
}

// may be omitted
func (s *service) Close() error {
	return nil
}

func main() {
	appservice.InitNewService(thisServiceName, &config{}, 2)
}
