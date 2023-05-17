package main

import (
	"context"
	"encoding/json"
	"fmt"
	"project/app/protocol"
	"project/wsconnector"
	"time"

	"github.com/gobwas/ws"
)

type headers struct {
	UserID     string `json:"appuid"`
	ChatID     string `json:"chatid"`
	LastUpdate int64  `json:"timestamp"` // ??????????????????????????переименовать
}

type foo struct{}

func (f foo) NewMessage() wsconnector.MessageReader {
	return &protocol.ClientMessage{}
}

func (foo) Handle(msg interface{}) error {
	message := msg.(*protocol.ClientMessage)
	headers := headers{}
	if len(message.Headers) != 0 {
		if err := json.Unmarshal(message.Headers, &headers); err != nil {
			fmt.Println("--------------------\nUnmarshalling error:", err.Error())
			return nil
		}
	}
	fmt.Println("--------------------\nNew message: \nType:", message.Type.String(), "\nAppUID:", message.ApplicationID, "\nTimestamp:", message.Timestamp, "\nHeaders:", headers, "\nBody byte:", message.Body, "\nBody string:", string(message.Body))
	return nil
}

func (foo) HandleClose(reason error) {
	fmt.Println("--------------------\nConn closed, reason:", reason.Error())
}

func main() {
	hdrsConnect, err := json.Marshal(headers{UserID: "123"})
	if err != nil {
		panic(err)
	}
	hdrsCreate, err := json.Marshal(headers{UserID: "123"})
	if err != nil {
		panic(err)
	}
	hdrsText, err := json.Marshal(headers{UserID: "123", ChatID: "1234"})
	if err != nil {
		panic(err)
	}

	wsconnector.SetupConnectionsEndpointSide(ws.StateClientSide)
	println("--------------------\nEndpoint setup")
	if err := wsconnector.SetupEpoll(nil); err != nil {
		panic(err)
	}
	println("--------------------\nEpoll setup")
	wsconn, err := wsconnector.Dial(context.Background(), "ws://127.0.0.1:9010", foo{})
	if err != nil {
		panic(err)
	}
	println("--------------------\nDialed, Wsconn created")
	if err := wsconn.StartServing(); err != nil {
		panic(err)
	}
	println("--------------------\nStarted serving, sleeping")
	time.Sleep(time.Second * 2)
	message, err := protocol.EncodeClientMessage(protocol.TypeSettingsReq, 0, 0, nil, nil)
	if err != nil {
		panic(err)
	}
	if err := wsconn.Send(message); err != nil {
		panic(err)
	}

	println("--------------------\nSettingsReq (index) sended, sleeping")
	time.Sleep(time.Second * 2)

	message, err = protocol.EncodeClientMessage(protocol.TypeSettingsReq, 1, 0, nil, nil)
	if err != nil {
		panic(err)
	}
	if err := wsconn.Send(message); err != nil {
		panic(err)
	}

	println("--------------------\nSettingsReq (app settings) sended, sleeping")
	time.Sleep(time.Second * 2)

	message, err = protocol.EncodeClientMessage(protocol.TypeRegistration, 1, 0, nil, nil)
	if err != nil {
		panic(err)
	}
	if err := wsconn.Send(message); err != nil {
		panic(err)
	}

	println("--------------------\nRegistration sended, sleeping")
	time.Sleep(time.Second * 2)

	message, err = protocol.EncodeClientMessage(protocol.TypeConnect, 1, 0, hdrsConnect, nil)
	if err != nil {
		panic(err)
	}
	if err := wsconn.Send(message); err != nil {
		panic(err)
	}

	println("--------------------\nConnect sended, sleeping")
	time.Sleep(time.Second * 2)

	message, err = protocol.EncodeClientMessage(protocol.TypeCreate, 1, 0, hdrsCreate, nil)
	if err != nil {
		panic(err)
	}
	if err := wsconn.Send(message); err != nil {
		panic(err)
	}

	println("--------------------\nCreate sended, sleeping")
	time.Sleep(time.Second * 2)

	message, err = protocol.EncodeClientMessage(protocol.TypeText, 1, 0, hdrsText, []byte("sometext"))
	if err != nil {
		panic(err)
	}
	if err := wsconn.Send(message); err != nil {
		panic(err)
	}

	println("--------------------\nText sended, sleeping")
	time.Sleep(time.Second * 20)
}
