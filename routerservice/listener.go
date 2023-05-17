package main

import (
	"net"
	"os"

	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"

	"strconv"
	"sync"
	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/wsconnector"
)

type listener struct {
	listener              net.Listener
	acceptedConnsToHandle chan net.Conn
	activeWorkers         chan struct{}

	clients *clientsConnsList
	apps    *applications

	servStatus *serviceStatus
	//configurator *configurator // если раскомменчивать - не забыть раскомментить строку в service.go после вызова newConfigurator()
	l         logger.Logger
	clients_l logger.Logger

	//ctx context.Context

	cancelAccept     chan struct{}
	acceptWorkerDone chan struct{}
	sync.RWMutex
}

const handlerCallTimeout time.Duration = time.Second * 10

func newListener(l logger.Logger, clients_l logger.Logger, servStatus *serviceStatus, apps *applications, clients *clientsConnsList, threads int) *listener {
	if threads < 1 {
		panic("threads num cant be less than 1")
	}
	return &listener{
		acceptedConnsToHandle: make(chan net.Conn, 1),
		activeWorkers:         make(chan struct{}, threads),
		clients:               clients,
		servStatus:            servStatus,
		apps:                  apps,
		l:                     l,
		clients_l:             clients_l,
	}
}

// TODO: я пока не придумал шо делать, если поднять листнер не удалось и мы ушли в суспенд (сейчас мы тупо не выйдем из суспенда)
// TODO: избавиться от засранности мьютексом
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

loop:
	for {
		select {
		case listener.activeWorkers <- struct{}{}:
			go listener.acceptHandlingWorker()
			continue loop
		default:
			break loop
		}
	}

	var err error
	if network == "unix" {
		if err = os.RemoveAll(address); err != nil {
			goto failure
		}
	}
	if listener.listener, err = net.Listen(network, address); err != nil {
		goto failure
	}

	listener.cancelAccept = make(chan struct{})
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

	timer := time.NewTimer(handlerCallTimeout)
	for {
		select {
		case <-listener.cancelAccept:
			listener.l.Debug("acceptWorker", "cancelAccept recieved, stop accept loop")
			timer.Stop()
			return
		default:
			conn, err := listener.listener.Accept()
			if err != nil {
				listener.l.Error("acceptWorker/Accept", err)
				continue
			}
			listener.l.Debug("acceptWorker/Accept", suckutils.ConcatTwo("accepced conn from: ", conn.RemoteAddr().String()))
			// if !listener.servStatus.onAir() { // на суспенд сервиса пока влияет только листнер, поэтому смысла не имеет
			// 	listener.l.Warning("acceptWorker", suckutils.ConcatTwo("suspended, discard handling conn from ", conn.RemoteAddr().String()))
			// 	conn.Close()
			// 	continue
			// }
		loop:
			for {
				timer.Reset(handlerCallTimeout)
				select {
				case <-time.After(handlerCallTimeout):
					listener.l.Warning("acceptWorker", suckutils.ConcatTwo("exceeded timeout, no free acceptHandlingWorker available for ", handlerCallTimeout.String()))
				case listener.acceptedConnsToHandle <- conn:
					break loop
				}
			}
		}
	}
}

func (listener *listener) acceptHandlingWorker() {
	listener.l.Debug("acceptHandlingWorker", "started")
	for conn := range listener.acceptedConnsToHandle {
		newclient, err := listener.clients.newClient()
		if err != nil {
			listener.l.Error("acceptHandlingWorker/newClient", err)
			conn.Close() // TODO: вернуть клиенту ошибку?
			continue
		}

		newclient.apps = listener.apps
		newclient.l = listener.clients_l.NewSubLogger(suckutils.ConcatTwo("ConnUID:", strconv.Itoa(int(newclient.connuid))), suckutils.ConcatTwo("Gen:", strconv.Itoa(int(newclient.curr_gen))))
		newclient.closehandler = func() error { return listener.clients.remove(newclient.connuid) }
		connector, err := wsconnector.NewWSConnector[protocol.AppServerMessage](conn, newclient)
		if err != nil {
			listener.l.Error("acceptHandlingWorker/NewWSConnector", err)
			listener.clients.remove(newclient.connuid)
			conn.Close()
			continue
		}

		newclient.conn = connector
		if err := connector.StartServing(); err != nil {
			listener.l.Error("StartServing", err)

			connector.ClearFromCache()
			listener.clients.remove(newclient.connuid)
			conn.Close()
			continue
		}

		listener.l.Debug("acceptHandlingWorker/Accept", suckutils.ConcatTwo("successfully upgraded to conn from: ", conn.RemoteAddr().String()))
	}
	<-listener.activeWorkers
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

	close(listener.cancelAccept)

	if err := listener.listener.Close(); err != nil {
		listener.l.Error("listener.stop()/listener.Close()", err)
	}

	<-listener.acceptWorkerDone

	listener.listener = nil

	listener.servStatus.setListenerStatus(false)
	listener.Unlock()
	listener.l.Debug("listener", "stopped")
	//listener.wg.Wait()
}

// calling close() we r closing listener forever (no further listen() calls) and waiting for all requests to be handled
func (listener *listener) close() {
	if listener == nil {
		panic("listener.close() called on nil listener")
	}
	listener.stop()
	listener.Lock()
	close(listener.acceptedConnsToHandle)
	timeout := time.NewTimer(time.Second * 10).C
loop:
	for i := 0; i < cap(listener.activeWorkers); i++ {
		select {
		case listener.activeWorkers <- struct{}{}:
			continue loop
		case <-timeout:
			listener.l.Debug("listener", "closed on timeout 10s (waited for acceptHandlingWorkers)")
			return
		}
	}
	listener.l.Debug("listener", "succesfully closed")
}

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
