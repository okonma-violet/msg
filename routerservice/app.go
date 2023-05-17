package main

import (
	"errors"

	"net"

	"sync"
	"time"

	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
)

type app struct {
	conns       []*connector.EpollReConnector[protocol.AppServerMessage, *protocol.AppServerMessage]
	appid       protocol.AppID
	servicename ServiceName
	clients     *clientsConnsList

	l logger.Logger
	sync.RWMutex
}

// sends only to the first successful sending, ignores other conns
func (a *app) SendToOne(message []byte) error {
	if len(message) < protocol.App_message_head_len && len(message) != 9 {
		return errors.New("message len does not satisfy neither app message min len nor timestamp message len")
	}

	a.l.Debug("Send", suckutils.ConcatTwo("message of type ", protocol.MessageType(message[0]).String()))

	a.RLock()
	defer a.RUnlock()
	for _, conn := range a.conns {
		if err := conn.Send(message); err != nil {
			a.l.Error("Send", err)
			continue
		}
		return nil
	}
	return errNoAliveConns
}

func (a *app) SendToAll(message []byte) {
	if len(message) < protocol.App_message_head_len && len(message) != 9 {
		a.l.Error("Send", errors.New("message len does not satisfy neither app message min len nor timestamp message len"))
		return
	}
	a.l.Debug("Send", suckutils.ConcatTwo("message of type ", protocol.MessageType(message[0]).String()))

	a.RLock()
	defer a.RUnlock()

	for _, conn := range a.conns {
		if err := conn.Send(message); err != nil {
			a.l.Error("Send", err)
			continue
		}

	}
}

func (a *app) connect(netw, addr string) {
	var recon *connector.EpollReConnector[protocol.AppServerMessage, *protocol.AppServerMessage]

	conn, err := net.DialTimeout(netw, addr, time.Second)
	if err != nil {
		a.l.Error("Dial", errors.New(suckutils.ConcatTwo("err catched, reconnect inited, err: ", err.Error())))
		goto conn_failed
	}
	if recon, err = connector.NewEpollReConnector[protocol.AppServerMessage](conn, a, nil, a.doAfterReconnect); err != nil {
		a.l.Error("NewEpollReConnector", errors.New(suckutils.ConcatTwo("err catched, reconnect inited, err: ", err.Error())))
		goto conn_failed
	}
	if err = recon.StartServing(); err != nil {
		recon.ClearFromCache()
		a.l.Error("StartServing", errors.New(suckutils.ConcatTwo("err catched, reconnect inited, err: ", err.Error())))
		goto conn_failed
	}
	goto conn_succeeded

conn_failed:
	recon = connector.AddToReconnectionQueue[protocol.AppServerMessage](netw, addr, a, nil, a.doAfterReconnect)

conn_succeeded:
	a.l.Info("Conn", suckutils.ConcatTwo("Connected to ", addr))

	a.conns = append(a.conns, recon)
}

func (a *app) doAfterReconnect() error {
	a.l.Debug("Conn", suckutils.ConcatTwo("succesfully reconnected to app \"", string(a.servicename)))
	return nil
}

func (a *app) Handle(message *protocol.AppServerMessage) error {

	a.l.Debug("Handle", suckutils.ConcatTwo("recieved message, messagetype: ", message.Type.String()))
	cl, err := a.clients.get(message.ConnectionUID, message.Generation)
	if err != nil {
		// TODO: send error?
		a.l.Error("Handle/GetClient", err)
		msg, _ := (&protocol.AppServerMessage{Type: protocol.TypeDisconnection, ConnectionUID: message.ConnectionUID, Generation: message.Generation, Timestamp: time.Now().UnixNano(), RawMessageData: make([]byte, 6)}).EncodeToAppMessage()
		a.SendToAll(msg)
		return nil
	}

	if message.Timestamp == 0 {
		message.Timestamp = time.Now().UnixNano() // TODO: Придумать поведение на случай сообщения без таймстампа
	}
	message.ApplicationID = a.appid

	if err = cl.send(message.EncodeToClientMessage()); err != nil {
		a.l.Error("Handle/Send", err)
		return nil
	}

	return nil
}

func (a *app) HandleClose(reason error) {
	a.l.Warning("Conn", suckutils.ConcatTwo("conn closed, reason err: ", reason.Error()))
}
