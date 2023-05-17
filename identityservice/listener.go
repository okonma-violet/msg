package identityserver

import (
	"context"
	"net"
	"os"
	"sync"

	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
)

type listener struct {
	listener net.Listener

	servStatus    *serviceStatus
	l             logger.Logger
	connections_l logger.Logger

	ctx context.Context

	cancelAccept     bool
	acceptWorkerDone chan struct{}
	sync.RWMutex
}

var handler Handler

var cur_connid uint16

// const handlerCallTimeout time.Duration = time.Second * 5
// const handlerCallMaxExceededTimeouts = 3

func newListener(ctx context.Context, l logger.Logger, connections_l logger.Logger, handler Handler, servStatus *serviceStatus) *listener {
	return &listener{
		ctx:           ctx,
		servStatus:    servStatus,
		cancelAccept:  false,
		l:             l,
		connections_l: connections_l,
	}
}

// TODO: я пока не придумал шо делать, если поднять листнер не удалось и мы ушли в суспенд (сейчас мы тупо не выйдем из суспенда)
func (listener *listener) listen(network, address string) error {
	if listener == nil {
		panic("listener.listen() called on nil listener")
	}
	listener.RLock()
	if listener.listener != nil {
		if listener.listener.Addr().String() == address {
			listener.RUnlock()
			return nil
		}
	}
	listener.RUnlock()
	listener.stop()

	listener.Lock()
	defer listener.Unlock()

	var err error
	if network == "unix" {
		if err = os.RemoveAll(address); err != nil {
			goto failure
		}
	}
	if listener.listener, err = net.Listen(network, address); err != nil {
		goto failure
	}

	listener.cancelAccept = false
	listener.acceptWorkerDone = make(chan struct{})
	go listener.acceptWorker()

	listener.servStatus.setListenerStatus(true)
	listener.l.Info("listen", suckutils.ConcatFour("start listening at ", network, ":", address))
	return nil
failure:
	listener.servStatus.setListenerStatus(false)
	return err
}

func (listener *listener) acceptWorker() {
	defer close(listener.acceptWorkerDone)
	for {
		conn, err := listener.listener.Accept()
		if err != nil {
			if listener.cancelAccept {
				listener.l.Debug("acceptWorker", "cancelAccept recieved, stop accept loop")
				return
			}
			listener.l.Error("acceptWorker/Accept", err)
			continue
		}

		if !listener.servStatus.onAir() {
			listener.l.Warning("acceptWorker", suckutils.ConcatTwo("service suspended, discard handling conn from ", conn.RemoteAddr().String()))
			conn.Close()
			continue
		}
		cni := &conninfo{l: listener.connections_l.NewSubLogger(suckutils.ConcatTwo("conn-", suckutils.Itoa(uint32(cur_connid))))}

		con, err := connector.NewEpollConnector[protocol.AppMessage](conn, cni)
		if err != nil {
			listener.l.Error("acceptWorker/NewEpollConnector", err)
			conn.Close()
			continue
		}
		if err = con.StartServing(); err != nil {
			listener.l.Error("acceptWorker/StartServing", err)
			conn.Close()
			con.ClearFromCache()
			continue
		}
		cur_connid++
		listener.l.Debug("acceptWorker", suckutils.ConcatTwo("connected from: ", conn.RemoteAddr().String()))

	}
}

// calling stop() we can call listen() again.
// и мы не ждем пока все отхэндлится
func (listener *listener) stop() {
	if listener == nil {
		panic("listener.stop() called on nil listener")
	}
	listener.Lock()
	if listener.listener == nil {
		listener.Unlock()
		return
	}

	listener.cancelAccept = true
	if err := listener.listener.Close(); err != nil {
		listener.l.Error("listener.stop()/listener.Close()", err)
	}
	<-listener.acceptWorkerDone
	listener.listener = nil

	listener.servStatus.setListenerStatus(false)
	listener.Unlock()
	listener.l.Debug("listener", "stopped")
}

// calling close() we r closing listener forever (no further listen() calls) and waiting for all reqests to be handled
// потенциальная дыра: вызов listener.close() при keepAlive=true и НЕ завершенном контексте (см. handlingWorker())
func (listener *listener) close() {
	if listener == nil {
		panic("listener.close() called on nil listener")
	}
	listener.stop()
	listener.Lock()

	listener.l.Debug("listener", "succesfully closed")
}

// func (listener *listener) onAir() bool {
// 	listener.RLock()
// 	defer listener.RUnlock()
// 	return listener.listener != nil
// }

func (listener *listener) Addr() (string, string) {
	if listener == nil {
		return "", ""
	}
	listener.RLock()
	defer listener.RUnlock()
	if listener.listener == nil {
		return "", ""
	}
	return listener.listener.Addr().Network(), listener.listener.Addr().String()
}
