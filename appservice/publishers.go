package appservice

import (
	"context"
	"errors"
	"net"
	"os"

	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
	"github.com/okonma-violet/msg/anystorage"
	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"
	"github.com/okonma-violet/services/types/configuratortypes"
)

type address struct {
	netw string
	addr string
}

type publishers struct {
	pubs_list   map[ServiceName]*Publisher
	idserv_list map[ServiceName]*IdentityServer
	mux         sync.Mutex
	pubupdates  chan pubupdate

	configurator *configurator
	l            logger.Logger
}

type Publisher struct {
	conn        *connector.EpollConnector[protocol.AppMessage, *protocol.AppMessage]
	servicename ServiceName
	addresses   []address
	current_ind int
	mux         sync.Mutex
	l           logger.Logger
}

var resp_HanldeFunc_storage anystorage.Storage[int64, func(*protocol.AppMessage)]
var resp_expires int64

type IdentityServer struct {
	pub          Publisher
	ClientID     string
	ClientSecret string

	keysfilemux sync.Mutex
}

type OuterConns_getter interface {
	GetPublisher(name ServiceName) *Publisher
	GetIdentityServer(name ServiceName) *IdentityServer
}

// type Publisher_Sender interface {
// 	SendHTTP(request *suckhttp.Request) (response *suckhttp.Response, err error)
// }

type pubupdate struct {
	name   ServiceName
	addr   address
	status configuratortypes.ServiceStatus
}

// call before configurator created
func newPublishers(ctx context.Context, l logger.Logger, servStatus *serviceStatus, configurator *configurator, pubscheckTicktime time.Duration, pubNames []ServiceName, idservsNames []ServiceName) (*publishers, error) {
	p := &publishers{configurator: configurator, l: l, pubs_list: make(map[ServiceName]*Publisher, len(pubNames)), idserv_list: make(map[ServiceName]*IdentityServer, len(idservsNames)), pubupdates: make(chan pubupdate, 1)}
	for _, pubname := range pubNames {
		if _, err := p.newPublisher(pubname); err != nil {
			return nil, err
		}
	}
	for _, idservname := range idservsNames {
		if _, err := p.newIdentityServer(idservname); err != nil {
			return nil, err
		}
	}
	go p.publishersWorker(ctx, servStatus, pubscheckTicktime)
	return p, nil

}

func (pubs *publishers) update(pubname ServiceName, netw, addr string, status configuratortypes.ServiceStatus) {
	pubs.pubupdates <- pubupdate{name: pubname, addr: address{netw: netw, addr: addr}, status: status}
}

func (pubs *publishers) publishersWorker(ctx context.Context, servStatus *serviceStatus, pubscheckTicktime time.Duration) {
	ticker := time.NewTicker(pubscheckTicktime)
loop:
	for {
		select {
		case <-ctx.Done():
			pubs.l.Debug("publishersWorker", "context done, exiting")
			return
		case update := <-pubs.pubupdates:

			// чешем мапу
			var pub *Publisher
			pubs.mux.Lock()
			if strings.HasPrefix(string(update.name), "idserv.") {
				if p, ok := pubs.idserv_list[update.name]; ok {
					pub = &p.pub
				} else {
					pubs.mux.Unlock()
					goto not_found
				}
			} else {
				if p, ok := pubs.pubs_list[update.name]; ok {
					pub = p
				} else {
					pubs.mux.Unlock()
					goto not_found
				}
			}
			pubs.mux.Unlock()

			// если есть в мапе
			// чешем список адресов
			for i := 0; i < len(pub.addresses); i++ {
				// если нашли в списке адресов
				if update.addr.netw == pub.addresses[i].netw && update.addr.addr == pub.addresses[i].addr {
					// если нужно удалять из списка адресов
					if update.status == configuratortypes.StatusOff || update.status == configuratortypes.StatusSuspended {
						pub.mux.Lock()

						pub.addresses = append(pub.addresses[:i], pub.addresses[i+1:]...)
						if pub.conn != nil {
							if pub.conn.RemoteAddr().String() == update.addr.addr {
								pub.conn.Close(errors.New("update from configurator"))
								pubs.l.Debug("publishersWorker", suckutils.ConcatFour("due to update, closed conn to \"", string(update.name), "\" from ", update.addr.addr))
							}
						}

						if pub.current_ind > i {
							pub.current_ind--
						}

						pub.mux.Unlock()

						pubs.l.Debug("publishersWorker", suckutils.Concat("pub \"", string(update.name), "\" from ", update.addr.addr, " updated to", update.status.String()))
						continue loop

					} else if update.status == configuratortypes.StatusOn { // если нужно добавлять в список адресов = ошибка, но может ложно стрельнуть при старте сервиса, когда при подключении к конфигуратору запрос на апдейт помимо хендшейка может отправить эта горутина по тикеру
						pubs.l.Error("publishersWorker", errors.New(suckutils.Concat("recieved pubupdate to status_on for already updated status_on for \"", string(update.name), "\" from ", update.addr.addr)))
						continue loop

					} else { // если кривой апдейт
						pubs.l.Error("publishersWorker", errors.New(suckutils.Concat("unknown statuscode: ", strconv.Itoa(int(update.status)), "at update pub \"", string(update.name), "\" from ", update.addr.addr)))
						continue loop
					}
				}
			}
			// если не нашли в списке адресов

			// если нужно добавлять в список адресов
			if update.status == configuratortypes.StatusOn {
				pub.mux.Lock()
				pub.addresses = append(pub.addresses, update.addr)
				pubs.l.Debug("publishersWorker", suckutils.Concat("added new addr ", update.addr.netw, ":", update.addr.addr, " for pub ", string(pub.servicename)))
				pub.mux.Unlock()
				continue loop

			} else if update.status == configuratortypes.StatusOff || update.status == configuratortypes.StatusSuspended { // если нужно удалять из списка адресов = ошибка
				pubs.l.Error("publishersWorker", errors.New(suckutils.Concat("recieved pubupdate to status_suspend/off for already updated status_suspend/off for \"", string(update.name), "\" from ", update.addr.addr)))
				continue loop

			} else { // если кривой апдейт = ошибка
				pubs.l.Error("publishersWorker", errors.New(suckutils.Concat("unknown statuscode: ", strconv.Itoa(int(update.status)), "at update pub \"", string(update.name), "\" from ", update.addr.addr)))
				continue loop
			}

		not_found: // если нет в мапе = ошибка и отписка
			pubs.l.Error("publishersWorker", errors.New(suckutils.ConcatThree("recieved update for non-publisher \"", string(update.name), "\", sending unsubscription")))

			pubname_byte := []byte(update.name)
			message := append(append(make([]byte, 0, 2+len(update.name)), byte(configuratortypes.OperationCodeUnsubscribeFromServices), byte(len(pubname_byte))), pubname_byte...)
			if err := pubs.configurator.send(connector.FormatBasicMessage(message)); err != nil {
				pubs.l.Error("publishersWorker/configurator.Send", err)
			}

		case <-ticker.C:
			empty_pubs := make([]string, 0, len(pubs.idserv_list)+len(pubs.idserv_list))
			empty_pubs_len := 0
			//pubs.rwmux.RLock()
			pubs.mux.Lock()
			for pub_name, pub := range pubs.pubs_list {
				pub.mux.Lock()
				if len(pub.addresses) == 0 {
					empty_pubs = append(empty_pubs, string(pub_name))
					empty_pubs_len += len(pub_name)
				}
				pub.mux.Unlock()
			}
			if len(empty_pubs) != 0 {
				servStatus.setPubsStatus(false)
			} else {
				servStatus.setPubsStatus(true)
			}
			for pub_name, idserv := range pubs.idserv_list {
				idserv.pub.mux.Lock()
				if len(idserv.pub.addresses) == 0 {
					empty_pubs = append(empty_pubs, string(pub_name))
					empty_pubs_len += len(pub_name)
				}
				idserv.pub.mux.Unlock()
			}
			pubs.mux.Unlock()

			if len(empty_pubs) != 0 {
				pubs.l.Warning("publishersWorker", suckutils.ConcatTwo("no publishers with names: ", strings.Join(empty_pubs, ", ")))
				message := make([]byte, 1, 1+empty_pubs_len+len(empty_pubs))
				message[0] = byte(configuratortypes.OperationCodeSubscribeToServices)
				for _, pubname := range empty_pubs {
					//check pubname len?
					message = append(append(message, byte(len(pubname))), []byte(pubname)...)
				}
				if err := pubs.configurator.send(connector.FormatBasicMessage(message)); err != nil {
					pubs.l.Error("Publishers", errors.New(suckutils.ConcatTwo("sending subscription to configurator error: ", err.Error())))
				}
			}
		}
	}
}

func (pubs *publishers) GetAllPubNames() []ServiceName {
	pubs.mux.Lock()
	defer pubs.mux.Unlock()
	res := make([]ServiceName, 0, len(pubs.pubs_list)+len(pubs.idserv_list))
	for pubname := range pubs.pubs_list {
		res = append(res, pubname)
	}
	for idservname := range pubs.idserv_list {
		res = append(res, idservname)
	}
	return res
}

func (pubs *publishers) GetPublisher(servicename ServiceName) *Publisher {
	if pubs == nil {
		return nil
	}
	pubs.mux.Lock()
	defer pubs.mux.Unlock()
	return pubs.pubs_list[servicename]
}

func (pubs *publishers) GetIdentityServer(servicename ServiceName) *IdentityServer {
	if pubs == nil {
		return nil
	}
	pubs.mux.Lock()
	defer pubs.mux.Unlock()
	return pubs.idserv_list[servicename]
}

func (pubs *publishers) newPublisher(name ServiceName) (*Publisher, error) {
	pubs.mux.Lock()
	defer pubs.mux.Unlock()

	if len(name) == 0 {
		return nil, errors.New("empty pubname")
	}

	if _, ok := pubs.pubs_list[name]; !ok {
		p := &Publisher{servicename: name, addresses: make([]address, 0, 1), l: pubs.l.NewSubLogger(string(name))}
		pubs.pubs_list[name] = p
		return p, nil
	} else {
		return nil, errors.New("publisher already initated")
	}
}
func (pubs *publishers) newIdentityServer(name ServiceName) (*IdentityServer, error) {
	pubs.mux.Lock()
	defer pubs.mux.Unlock()

	if len(name) == 0 {
		return nil, errors.New("empty idservname")
	}

	if _, ok := pubs.idserv_list[name]; !ok {
		idsrv := &IdentityServer{pub: Publisher{servicename: name, addresses: make([]address, 0, 1), l: pubs.l.NewSubLogger(string(name))}}
		pubs.idserv_list[name] = idsrv
		return idsrv, nil
	} else {
		return nil, errors.New("idserv already initated")
	}
}

// resp_params_dstn - destination for unmarshalling response params (see protocol/oauth.go) on success
func (pub *Publisher) Send(message *protocol.AppMessage, response_handlefunc func(resp_message *protocol.AppMessage)) error {
	message_byte, err := message.Byte()
	if err != nil {
		return err
	}
	if err = resp_HanldeFunc_storage.Add(message.Timestamp, response_handlefunc, resp_expires); err != nil {
		return err
	}

	pub.mux.Lock()
	defer pub.mux.Unlock()

	if pub.conn != nil {
		err = pub.conn.Send(message_byte)
	}
	if pub.conn == nil || err != nil {
		if err != nil {
			pub.l.Error("Send", err)
		} else {
			pub.l.Debug("Conn", "not connected, reconnect")
		}
		if err = pub.connect(); err == nil {
			if err = pub.conn.Send(message_byte); err != nil {
				resp_HanldeFunc_storage.Remove(message.Timestamp)
				return err
			}
		} else {
			resp_HanldeFunc_storage.Remove(message.Timestamp)
			return err
		}
	}
	return nil
}

func (idserv *IdentityServer) Send(message *protocol.AppMessage, response_handlefunc func(*protocol.AppMessage)) error {
	return idserv.pub.Send(message, response_handlefunc)
}

// no mutex inside
func (pub *Publisher) connect() error {
	if pub.conn != nil {
		pub.conn.Close(errors.New("connect() call"))
		pub.conn = nil
	}

	for i := 0; i < len(pub.addresses); i++ {
		if pub.current_ind == len(pub.addresses) {
			pub.current_ind = 0
		}
		if conn, err := net.DialTimeout(pub.addresses[pub.current_ind].netw, pub.addresses[pub.current_ind].addr, time.Second); err != nil {
			pub.l.Error("connect/Dial", err)
		} else {
			if connctr, err := connector.NewEpollConnector[protocol.AppMessage](conn, pub); err == nil {
				pub.conn = connctr
				if err = connctr.StartServing(); err == nil {
					pub.l.Info("Conn", suckutils.ConcatTwo("Connected to ", pub.conn.RemoteAddr().String()))
					return nil
				} else {
					pub.l.Error("connect/StartServing", err)
				}
			} else {
				pub.l.Error("connect/NewEpollConnector", err)
			}
		}
		pub.current_ind++
	}
	return errors.New("no alive addresses")
}

// TODO: если приложение зарегалось, но проебало appid и/или secret - шо делать? + если не смогли сохранить в файл ключи - шо делать?
func (idserv *IdentityServer) connect() error {
	return idserv.pub.connect()
}

func (idserv *IdentityServer) saveAppKeys() error {
	idserv.keysfilemux.Lock()
	defer idserv.keysfilemux.Unlock()
	file, err := os.OpenFile("idservs_keys.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0664)
	//file, err := suckutils.OpenConcurrentFile(context.Background(), "idservs_keys.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0664, time.Second*5)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(suckutils.Concat(string(idserv.pub.servicename), " ", idserv.ClientID, " ", idserv.ClientSecret, "\n"))
	return err
}

func (p *Publisher) Handle(message *protocol.AppMessage) error {
	if hfunc, err := resp_HanldeFunc_storage.Get(message.Timestamp); err != nil {
		p.l.Warning("Response_HandleFunc_Storage", err.Error())
	} else {
		hfunc(message)
	}
	return nil
}
func (p *Publisher) HandleClose(reason error) {
	p.mux.Lock()
	p.l.Warning("Conn", suckutils.ConcatTwo("closed, reason: ", reason.Error()))
	p.conn = nil
	p.mux.Unlock()
}
