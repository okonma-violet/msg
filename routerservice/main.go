package main

import (
	"context"
	"encoding/json"
	"errors"

	"os"
	"os/signal"

	"syscall"
	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/confdecoder"
	"github.com/okonma-violet/connector"
	"github.com/okonma-violet/dynamicworkerspool"
	"github.com/okonma-violet/services/logs/encode"
	"github.com/okonma-violet/services/logs/logger"

	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/wsconnector"
)

type ServiceName string

type file_config struct {
	ConfiguratorAddr string
}

const pubscheckTicktime time.Duration = time.Second * 5
const reconnectTimeout time.Duration = time.Second * 10
const clientsConnectionsLimit = 100 // max = 16777215
const clientsServingThreads_min = 2
const clientsServingThreads_max = 5
const clientsServingThread_killing_timeout = time.Second * 5
const appsServingThreads_min = 2
const appsServingThreads_max = 5
const appsServingThread_killing_timeout = time.Second * 5
const listenerAcceptThreads = 2

// TODO: конфигурировать кол-во горутин-хендлеров конфигуратором

const thisservicename ServiceName = "applicationserver"

func main() {
	servconf := &file_config{}
	pfd, err := confdecoder.ParseFile("config.txt")
	if err != nil {
		panic("parsing config.txt err: " + err.Error())
	}
	if err := pfd.DecodeTo(servconf, servconf); err != nil {
		panic("decoding config.txt err: " + err.Error())
	}

	if servconf.ConfiguratorAddr == "" {
		panic("ConfiguratorAddr in config.txt not specified")
	}
	pfdapps, err := confdecoder.ParseFile("apps.index")
	if err != nil {
		panic("parsing apps.index err: " + err.Error())
	}
	if len(pfdapps.Keys()) == 0 {
		panic("no apps to work with (empty apps.index)")
	}

	ctx, cancel := createContextWithInterruptSignal()

	logsflusher := logger.NewFlusher(encode.DebugLevel)
	l := logsflusher.NewLogsContainer(string(thisservicename))

	wsconnector.SetEpoll(connector.SetupEpoll(func(e error) {
		l.Error("epoll OnWaitError", e)
		cancel()
	}))
	connector.SetupReconnection(ctx, reconnectTimeout, ((len(pfd.Keys) / 2) + 1), 2)
	apool := dynamicworkerspool.NewPool(appsServingThreads_min, appsServingThreads_max, appsServingThread_killing_timeout)
	connector.SetupPoolHandling(apool)
	cpool := dynamicworkerspool.NewPool(clientsServingThreads_min, clientsServingThreads_max, clientsServingThread_killing_timeout)
	wsconnector.SetupPoolHandling(cpool)

	servStatus := newServiceStatus()

	appslist := make([]struct {
		AppID   protocol.AppID `json:"appid"`
		AppName string         `json:"appname"`
	}, len(pfdapps.Keys))
	apps, startAppsUpdateWorker := newApplications(ctx, l.NewSubLogger("Apps"), nil, nil, pubscheckTicktime, len(pfdapps.Keys))

	clients := newClientsConnsList(clientsConnectionsLimit, apps)
	for i, appname := range pfdapps.Keys {
		appsettings, err := os.ReadFile(suckutils.ConcatTwo(appname, ".settings"))
		if err != nil {
			panic(err) // TODO:???????
		}
		appslist[i] = struct {
			AppID   protocol.AppID `json:"appid"`
			AppName string         `json:"appname"`
		}{AppID: protocol.AppID(i + 1), AppName: appname}

		if _, err := apps.newApp(protocol.AppID(i+1), appsettings, clients, ServiceName(appname)); err != nil {
			panic(err)
		}
	}
	appsIndex, err := json.Marshal(appslist)
	if err != nil {
		panic(err)
	}
	apps.appsIndex = appsIndex

	ln := newListener(l.NewSubLogger("Listener"), l.NewSubLogger("Client"), servStatus, apps, clients, listenerAcceptThreads)

	conf := newConfigurator(ctx, l.NewSubLogger("Configurator"), startAppsUpdateWorker, servStatus, apps, ln, servconf.ConfiguratorAddr, thisservicename)
	apps.configurator = conf
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

	cpool.Close()
	apool.Close()
	if err = cpool.DoneWithTimeout(time.Second * 5); err != nil {
		l.Error("Gopool", errors.New("break gopool.done clients waiting: timed out"))
	}
	if err = cpool.DoneWithTimeout(time.Second * 5); err != nil {
		l.Error("Gopool", errors.New("break gopool.done (apps) waiting: timed out"))
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
