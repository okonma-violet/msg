package main

import (
	"context"
	"errors"

	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"
	"github.com/okonma-violet/services/types/configuratortypes"

	"strconv"

	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
)

// TODO: при обычном коннекторе (не реконнекторе, как здесь), когда мы получаем обновление от конфигуратора о нерабочем статусе
// паблишера (суспенд/офф) мы удаляем его из списка подключений. Но, так как здесь реконнектор - мы этого не делаем, так как реконнектор все равно переподрубится
// при поднятии паблишера. Если тереть из app{}, то при реконнете, то подключения будет два, и одно из них отсутствует в app{}
// Тогда, если паблишер переехал на другой адрес, то у нас мертвый реконнектор в app{} + мертвый груз в буфере реконнектора ждет диал (отлетает по таймауту).
// Че делать? (новый статускод? отказ от реконнектора в нынешнем виде?)

var errNoAliveConns = errors.New("no alive conns")

type applications struct {
	list []app // zero index is always nil
	//sync.RWMutex
	settingslist [][]byte // хз какой формат
	appsIndex    []byte   // json

	appupdates chan appupdate

	configurator *configurator
	l            logger.Logger
}

type appupdate struct {
	name   ServiceName
	netw   string
	addr   string
	status configuratortypes.ServiceStatus
}

// call before configurator created
func newApplications(ctx context.Context, l logger.Logger, appsIndex []byte, configurator *configurator, pubscheckTicktime time.Duration, size int) (*applications, func()) {
	a := &applications{
		configurator: configurator,
		l:            l,
		list:         make([]app, size+1),
		settingslist: make([][]byte, size+1),
		appupdates:   make(chan appupdate, 1),
		appsIndex:    appsIndex,
	}

	updateWorkerStart := make(chan struct{})

	go a.appsUpdateWorker(ctx, l.NewSubLogger("AppsUpdateWorker"), updateWorkerStart, pubscheckTicktime)
	return a, func() { close(updateWorkerStart) }
}

func (apps *applications) newApp(appid protocol.AppID, settings []byte, clients *clientsConnsList, appname ServiceName) (*app, error) {
	// apps.Lock()
	// defer apps.Unlock()

	if appid == 0 {
		return nil, errors.New("zero appID")
	}
	if int(appid) >= len(apps.list) {
		return nil, errors.New("weird appID (appID is bigger than num of apps)")
	}
	if settings == nil {
		return nil, errors.New("nil settings")
	}

	if apps.list[appid].conns == nil {
		apps.list[appid] = app{
			servicename: appname,
			appid:       appid,
			conns:       make([]*connector.EpollReConnector[protocol.AppServerMessage, *protocol.AppServerMessage], 0, 1),
			clients:     clients,
			l:           apps.l.NewSubLogger("App", suckutils.ConcatTwo("AppID:", strconv.Itoa(int(appid))), string(appname)),
		}

		apps.settingslist[appid] = settings

		return &apps.list[appid], nil
	} else {
		return nil, errors.New("app is already created")
	}
}

func (apps *applications) update(appname ServiceName, netw, addr string, status configuratortypes.ServiceStatus) {
	apps.appupdates <- appupdate{name: appname, netw: netw, addr: addr, status: status}
}

func (apps *applications) appsUpdateWorker(ctx context.Context, l logger.Logger, updateWorkerStart <-chan struct{}, appsscheckTicktime time.Duration) {
	<-updateWorkerStart
	l.Debug("UpdateLoop", "started")
	ticker := time.NewTicker(pubscheckTicktime)
loop:
	for {
		select {
		case <-ctx.Done():
			l.Debug("Context", "context done, exiting")
			return
		case update := <-apps.appupdates:
			//apps.RLock()
			// чешем список
			for i := 1; i < len(apps.list); i++ {
				if apps.list[i].servicename == update.name {
					// если есть в списке
					//apps.RUnlock()

					// чешем список подключений
					for k := 0; k < len(apps.list[i].conns); k++ {
						// если нашли в списке подключений
						if apps.list[i].conns[k].RemoteAddr().String() == update.addr {
							// если нужно отрубать
							if update.status == configuratortypes.StatusOff || update.status == configuratortypes.StatusSuspended {
								// СМОТРИ TODO: В НАЧАЛЕ ФАЙЛА

								// apps.list[i].Lock()
								// apps.list[i].conns[k].CancelReconnect()
								// apps.list[i].conns[k].Close(errors.New("update from configurator"))
								// //apps.list[i].conns = append(apps.list[i].conns[:k], apps.list[i].conns[k+1:]...)
								// apps.list[i].conns = apps.list[i].conns[:k+copy(apps.list[i].conns[k:], apps.list[i].conns[k+1:])]
								// l.Debug("Update", suckutils.ConcatFour("due to update, closed conn to \"", string(apps.list[i].servicename), "\" from ", update.addr))

								// apps.list[i].Unlock()

								l.Debug("Update", suckutils.Concat("app \"", string(apps.list[i].servicename), "\" from ", update.addr, " updated to ", update.status.String()))
								continue loop

							} else if update.status == configuratortypes.StatusOn { // если нужно подрубать = ошибка
								l.Error("Update", errors.New(suckutils.Concat("appupdate to status_on for already updated status_on for \"", string(apps.list[i].servicename), "\" from ", update.addr)))
								continue loop

							} else { // если кривой апдейт = ошибка
								l.Error("Update", errors.New(suckutils.Concat("unknown statuscode: \"", strconv.Itoa(int(update.status)), "\" at update app \"", string(apps.list[i].servicename), "\" from ", update.addr)))
								continue loop
							}
						}
					}

					// если не нашли в списке подключений:

					// если нужно подрубать
					if update.status == configuratortypes.StatusOn {
						apps.list[i].Lock()
						apps.list[i].connect(update.netw, update.addr)
						apps.list[i].Unlock()
						continue loop
					} else if update.status == configuratortypes.StatusOff || update.status == configuratortypes.StatusSuspended { // если нужно отрубать = ошибка
						l.Error("Update", errors.New(suckutils.Concat("appupdate to status_off for already updated status_off for \"", string(apps.list[i].servicename), "\" from ", update.addr)))
						continue loop

					} else { // если кривой апдейт = ошибка
						l.Error("Update", errors.New(suckutils.Concat("unknown statuscode: ", strconv.Itoa(int(update.status)), "at update pub \"", string(apps.list[i].servicename), "\" from ", update.addr)))
						continue loop
					}
				}
			}
			//apps.RUnlock()

			// если нет в списке = ошибка и отписка

			l.Error("Update", errors.New(suckutils.Concat("appupdate for non-subscription \"", string(update.name), "\", sending unsubscription")))

			appname_byte := []byte((update.name))
			message := append(append(make([]byte, 0, 2+len(appname_byte)), byte(configuratortypes.OperationCodeUnsubscribeFromServices), byte(len(appname_byte))), appname_byte...)
			if err := apps.configurator.send(message); err != nil {
				l.Error("configurator.Send", err)
			}

		case <-ticker.C:
			empty_appsnames := make([]ServiceName, 0, len(apps.list)-1)
			empty_appnames_totallen := 0
			//apps.RLock()
			for i := 1; i < len(apps.list); i++ {
				apps.list[i].RLock()
				if len(apps.list[i].conns) == 0 {
					empty_appsnames = append(empty_appsnames, apps.list[i].servicename)
					empty_appnames_totallen += len(apps.list[i].servicename)
				}
				apps.list[i].RUnlock()
			}
			if len(empty_appsnames) == 0 {
				continue loop
			}
			//apps.RUnlock()
			message := make([]byte, 1, empty_appnames_totallen+len(empty_appsnames)+1)
			message[0] = byte(configuratortypes.OperationCodeSubscribeToServices)
			for _, pub_name := range empty_appsnames {
				pub_name_byte := []byte(pub_name)
				message = append(append(message, byte(len(pub_name_byte))), pub_name_byte...)
			}
			if err := apps.configurator.send(message); err != nil {
				l.Error("configurator.Send", err)
			}
		}
	}
}

// func (apps *applications) GetAllAppsIDs() []appID {
// 	apps.RLock()
// 	defer apps.RUnlock()
// 	res := make([]appID, 0, len(apps.list))
// 	for appid := range apps.list {
// 		res = append(res, appid)
// 	}
// 	return res
// }

func (apps *applications) getAllAppNames() []ServiceName {
	// apps.RLock()
	// defer apps.RUnlock()
	res := make([]ServiceName, 0, len(apps.list)-1)
	for i := 1; i < len(apps.list); i++ {
		res = append(res, apps.list[i].servicename)
	}
	return res
}

func (apps *applications) get(appid protocol.AppID) (*app, error) {
	if appid == 0 || int(appid) >= len(apps.list) {
		return nil, errors.New(suckutils.ConcatThree("impossible appid (must be 0<connuid<=len(apps.list)): \"", strconv.Itoa(int(appid)), "\""))
	}
	return &apps.list[appid], nil
}

func (apps *applications) getSettings(appid protocol.AppID) ([]byte, error) {
	if appid == 0 || int(appid) >= len(apps.list) {
		return nil, errors.New(suckutils.ConcatThree("impossible appid (must be 0<connuid<=len(apps.list)): \"", strconv.Itoa(int(appid)), "\""))
	}
	return apps.settingslist[appid], nil
}

func (apps *applications) CheckAvailable(appid protocol.AppID) bool {
	if appid == 0 || int(appid) >= len(apps.list) {
		return false
	}
	return true
}

func (apps *applications) SendToAll(message []byte) {
	// apps.RLock()
	// defer apps.RUnlock()
	for i := 0; i < len(apps.list); i++ {
		apps.list[i].SendToAll(message)
	}
}
