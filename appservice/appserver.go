package appservice

import (
	"context"
	"errors"

	"strconv"
	"sync"
	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"
)

type Sender interface {
	Send(message []byte) error
}

type handleFunc func(*protocol.AppMessage) error

type appserver struct {
	conn       *connector.EpollConnector[protocol.AppMessage, *protocol.AppMessage]
	handlefunc handleFunc

	sendQueue    chan []byte
	backingQueue chan []byte

	l logger.Logger

	connAlive bool
	sync.RWMutex
}

const backingQueue_size = 1

func newAppService(ctx context.Context, l logger.Logger, sendqueueSize int, handlefunc handleFunc) *appserver {
	as := &appserver{
		handlefunc:   handlefunc,
		sendQueue:    make(chan []byte, sendqueueSize),
		backingQueue: make(chan []byte, backingQueue_size),
		l:            l,
	}
	go as.sendWorker(ctx)

	return as
}

func (as *appserver) Handle(message *protocol.AppMessage) error {
	if err := as.handlefunc(message); err != nil {
		as.l.Error("Handle", err)
	}
	return nil
}

func (as *appserver) Send(message []byte) error {
	// TODO: add timeout?
	if len(message) < protocol.App_message_head_len {
		return errors.New("message len does not satisfy minimal length")
	}
	as.sendQueue <- message
	return nil
}

func (as *appserver) sendWorker(ctx context.Context) {
send_loop:
	for {
		select {
		case message := <-as.backingQueue:
			as.l.Debug("sendWorker", "retrying to send a message")
			if err := as.conn.Send(message); err != nil {
				as.l.Error("sendWorker/Send", err)
				as.backingQueue <- message

				as.RLock()
				if !as.connAlive {
					as.l.Debug("sendWorker", "conn to appserver is dead, timeout")
					as.RUnlock()
					time.Sleep(time.Second * 5)
					continue send_loop
				}
				as.RUnlock()
			}
		case <-ctx.Done():
			break send_loop
		default:
			select {
			case message := <-as.sendQueue:
				as.l.Debug("sendWorker", "sending a message")
				if err := as.conn.Send(message); err != nil {
					as.l.Error("sendWorker/Send", err)
					as.backingQueue <- message
				}
			case <-ctx.Done():
				break send_loop
			}
		}
	}
	as.l.Debug("sendWorker", "context done, send loop terminated")

	//TODO: queue dump?
	var unsended_messages int
	for range as.backingQueue {
		unsended_messages++
	}

	for range as.sendQueue {
		unsended_messages++
	}
	as.l.Debug("sendWorker", suckutils.ConcatTwo("exiting, unsended messages: ", strconv.Itoa(unsended_messages)))
}

func (as *appserver) HandleClose(reason error) {
	if reason != nil {
		as.l.Warning("Conn", suckutils.ConcatTwo("closed, reason: ", reason.Error()))
	} else {
		as.l.Warning("Conn", "closed, no reason specified")
	}

	as.Lock()
	defer as.Unlock()
	as.connAlive = false
}
