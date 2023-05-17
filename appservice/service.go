package appservice

import (
	"context"

	"errors"
	"os"
	"os/signal"

	"syscall"
	"time"

	"github.com/okonma-violet/confdecoder"
	"github.com/okonma-violet/connector"
	"github.com/okonma-violet/dynamicworkerspool"
	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/encode"
	"github.com/okonma-violet/services/logs/logger"
)

type ServiceName string

type Servicier interface {
	CreateHandler(ctx context.Context, l logger.Logger, sender Sender, outer_conns OuterConns_getter) (Handler, error)
}

type Handler interface {
	Handle(message *protocol.AppMessage) error
}

type closer interface {
	Close() error
}

type file_config struct {
	ConfiguratorAddr string
}

const pubscheckTicktime time.Duration = time.Second * 5
const reconnectionTimeout time.Duration = time.Second * 5
const sendQueueSize = 5

// TODO: придумать шото для неторчащих наружу сервисов

func InitNewService(servicename ServiceName, config Servicier, min_handlethreads, max_handlingthreads int, threadkilling_timeout time.Duration, publishers_names []ServiceName, identity_servers_names []ServiceName) {
	servconf := &file_config{}
	pfd, err := confdecoder.ParseFile("config.txt")
	if err != nil {
		panic("parsing config.txt err: " + err.Error())
	}
	if err = pfd.DecodeTo(servconf, config); err != nil {
		panic("decoding config.txt err: " + err.Error())
	}

	if servconf.ConfiguratorAddr == "" {
		panic("ConfiguratorAddr in config.txt not specified")
	}

	ctx, cancel := createContextWithInterruptSignal()

	logsflusher := logger.NewFlusher(encode.DebugLevel)
	l := logsflusher.NewLogsContainer(string(servicename))

	servStatus := newServiceStatus()

	connector.SetupEpoll(func(e error) {
		l.Error("epoll OnWaitError", e)
		cancel()
	})
	connector.SetupReconnection(ctx, reconnectionTimeout, 1, 1)
	pool := dynamicworkerspool.NewPool(min_handlethreads, max_handlingthreads, threadkilling_timeout)
	connector.SetupPoolHandling(pool)

	var pubs *publishers

	if len(publishers_names) != 0 {
		if pubs, err = newPublishers(ctx, l.NewSubLogger("Publishers"), servStatus, nil, pubscheckTicktime, publishers_names, identity_servers_names); err != nil {
			panic(err)
		}
	} else {
		servStatus.setPubsStatus(true)
	}
	appserv := newAppService(ctx, l.NewSubLogger("AppServer"), sendQueueSize, nil)

	handler, err := config.CreateHandler(ctx, l.NewSubLogger("Handler"), appserv, pubs)
	if err != nil {
		panic(err)
	}

	appserv.handlefunc = handler.Handle

	ln := newListener(ctx, l.NewSubLogger("Listener"), appserv, servStatus)

	conf := newConfigurator(ctx, l.NewSubLogger("Configurator"), servStatus, pubs, ln, servconf.ConfiguratorAddr, servicename)
	if pubs != nil {
		pubs.configurator = conf
	}

	//ln.configurator = configurator
	servStatus.setOnSuspendFunc(conf.onSuspend)
	servStatus.setOnUnSuspendFunc(conf.onUnSuspend)

	select {
	case <-ctx.Done():
		l.Info("Shutdown", "reason: context done")
		break
	case <-conf.terminationByConfigurator:
		l.Info("Shutdown", "reason: termination by configurator")
		break
	}
	ln.close()
	if closehandler, ok := handler.(closer); ok {
		if err = closehandler.Close(); err != nil {
			l.Error("CloseFunc", err)
		}
	}
	pool.Close()
	if err = pool.DoneWithTimeout(time.Second * 5); err != nil {
		l.Error("Gopool", errors.New("break gopool done waiting: timed out"))
	}
	logsflusher.Close()
	<-logsflusher.Done()
}

func createContextWithInterruptSignal() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		cancel()
	}()
	return ctx, cancel
}
