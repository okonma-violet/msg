package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"

	"strconv"
	"sync"

	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/msg/protocol"
	"github.com/okonma-violet/services/logs/logger"
	"github.com/okonma-violet/wsconnector"
)

type clientsConnsList struct {
	conns []client

	sync.RWMutex
}

type client struct {
	connuid      protocol.ConnUID
	curr_gen     byte
	conn         wsconnector.Conn
	closehandler func() error
	apps         *applications

	l logger.Logger
	sync.Mutex
}

const connslist_max_freeconnuid_scan_iterations = 2
const connslist_freeconnuid_scan_timeout = time.Second * 2

func newClientsConnsList(size int, apps *applications) *clientsConnsList {
	if size == 0 || size+1 > protocol.AppServer_MaxConnUID {
		panic("clients list impossible size (must be 0<size<protocol.Max_ConnUID-1)")
	}
	cc := &clientsConnsList{conns: make([]client, size+1)}
	for i := 1; i < len(cc.conns); i++ {
		cc.conns[i].connuid = protocol.ConnUID(i)
		cc.conns[i].apps = apps
	}
	return cc
}

func (cc *clientsConnsList) newClient() (*client, error) {
	cc.Lock()
	defer cc.Unlock()
	for iter := 0; iter < connslist_max_freeconnuid_scan_iterations; iter++ {
		for i := 1; i < len(cc.conns); i++ {
			cc.conns[i].Lock()
			if cc.conns[i].conn == nil {
				cc.conns[i].conn = &wsconnector.EpollWSConnector[protocol.AppServerMessage, *protocol.AppServerMessage]{}
				cc.conns[i].curr_gen++

				cc.conns[i].Unlock()

				return &cc.conns[i], nil
			}
			cc.conns[i].Unlock()
		}
		cc.Unlock()
		time.Sleep(connslist_freeconnuid_scan_timeout) // иначе connuid не освободится из-за мьютекса
		cc.Lock()

	}
	return nil, errors.New("no free permitted connections")
}

func (cc *clientsConnsList) remove(connuid protocol.ConnUID) error {
	cc.Lock()
	defer cc.Unlock()
	if connuid == 0 || int(connuid) >= len(cc.conns) {
		return errors.New(suckutils.ConcatThree("impossible connuid (must be 0<connuid<=len(cc.conns)): \"", strconv.Itoa(int(connuid)), "\""))
	}
	cc.conns[connuid].Lock()
	cc.conns[connuid].conn = nil
	cc.conns[connuid].Unlock()
	return nil
}

// returns nil client on not found
func (cc *clientsConnsList) get(connuid protocol.ConnUID, generation byte) (*client, error) {
	if connuid == 0 || int(connuid) >= len(cc.conns) {
		return nil, errors.New(suckutils.ConcatThree("impossible connuid (must be 0<connuid<=len(cc.conns)): \"", strconv.Itoa(int(connuid)), "\""))
	}
	cc.RLock()
	defer cc.RUnlock()

	cc.conns[connuid].Lock()
	defer cc.conns[connuid].Unlock()

	if cc.conns[connuid].conn != nil {
		if cc.conns[connuid].curr_gen == generation {
			return &cc.conns[connuid], nil
		}
		return nil, errors.New("client not found, generation mismatch")
	}
	return nil, errors.New("client not found, such connuid is dead")
}

// wsservice.Handler {wsconnector.WsHandler} interface implementation
func (cl *client) Handle(message *protocol.AppServerMessage) error {
	//cl.l.Info("NEW MESSAGE", fmt.Sprint(msg))

	cl.Lock()
	ts := time.Now().UnixNano()

	if message.Type == protocol.TypeSettingsReq {
		if message.ApplicationID == 0 {
			hdrs, _ := json.Marshal(struct {
				C string `json:"content-type"`
			}{C: "appsindex"})

			clmsg, _ := protocol.EncodeClientMessage(protocol.TypeSettingsReq, 0, ts, hdrs, cl.apps.appsIndex)

			err := cl.conn.Send(clmsg)
			if err != nil {
				cl.l.Error("Send", err)
			}
			cl.Unlock()
			return err
		} else {
			if body, err := cl.apps.getSettings(message.ApplicationID); err != nil {
				cl.l.Error("GetSettings", err)
				cl.Unlock()
				return err
			} else {
				hdrs, _ := json.Marshal(struct {
					C string `json:"content-type"`
				}{C: "appsettings"})

				clmsg, _ := protocol.EncodeClientMessage(protocol.TypeSettingsReq, 0, ts, hdrs, body)

				err := cl.conn.Send(clmsg)
				if err != nil {
					cl.l.Error("Send", err)
				}
				cl.Unlock()
				return err
			}
		}
	}
	app, err := cl.apps.get(message.ApplicationID)
	if err != nil {
		// TODO: send UpdateSettings?
		cl.l.Error("Handle/Message.ApplicationID", err)
		cl.Unlock()
		return nil
	}

	ts_message := make([]byte, 9)
	ts_message[0] = byte(protocol.TypeTimestamp)
	binary.BigEndian.PutUint64(ts_message[1:], uint64(ts))
	if err := cl.conn.Send(ts_message); err != nil {
		cl.l.Error("Send", err)
		cl.Unlock()
		return err
	}
	cl.Unlock()

	message.Timestamp = ts
	message.ConnectionUID = cl.connuid
	message.Generation = cl.curr_gen
	appmessage, err := message.EncodeToAppMessage()
	if err != nil {
		panic(err)
	}

	app.SendToAll(appmessage)
	// TODO: успешность отправки сообщить клиенту?
	return nil
}

func (cl *client) send(message []byte) error {
	if len(message) < protocol.Client_message_head_len && len(message) != 9 {
		return errors.New("message len does not satisfy neither client message min len nor timestamp message len")
	}
	cl.Lock()
	defer cl.Unlock()

	if cl.conn != nil {
		cl.l.Debug("Send", suckutils.ConcatTwo("message of type ", protocol.MessageType(message[0]).String()))
		return cl.conn.Send(message)
	} else {
		return wsconnector.ErrNilConn
	}
}

// wsservice.Handler {wsconnector.WsHandler} interface implementation
func (cl *client) HandleClose(err error) {
	cl.l.Debug("Conn", suckutils.ConcatTwo("closed, reason: ", err.Error()))
	// TODO: send disconnection? но ому конкретно? можно всем
	msg, _ := (&protocol.AppServerMessage{Type: protocol.TypeDisconnection, ConnectionUID: cl.connuid, Generation: cl.curr_gen, Timestamp: time.Now().UnixNano(), RawMessageData: make([]byte, 6)}).EncodeToAppMessage()
	cl.apps.SendToAll(msg)
	if cl.closehandler != nil {
		if err := cl.closehandler(); err != nil {
			cl.l.Error("Conn", errors.New(suckutils.ConcatTwo("error on closehandler, err: ", err.Error())))
		}
	}
}
