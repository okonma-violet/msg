package identityserver

import (
	"context"
	"errors"
	"net"

	"github.com/okonma-violet/services/logs/logger"

	"strings"
	"time"

	"github.com/big-larry/suckutils"
	"github.com/okonma-violet/connector"
	"github.com/okonma-violet/services/types/configuratortypes"
	"github.com/okonma-violet/services/types/netprotocol"
)

type configurator struct {
	conn            *connector.EpollReConnector[connector.BasicMessage, *connector.BasicMessage]
	thisServiceName ServiceName

	publishers *publishers
	listener   *listener
	servStatus *serviceStatus

	terminationByConfigurator chan struct{}
	l                         logger.Logger
}

func newConfigurator(ctx context.Context, l logger.Logger, servStatus *serviceStatus, pubs *publishers, listener *listener, configuratoraddr string, thisServiceName ServiceName) *configurator {

	c := &configurator{
		thisServiceName:           thisServiceName,
		l:                         l,
		servStatus:                servStatus,
		publishers:                pubs,
		listener:                  listener,
		terminationByConfigurator: make(chan struct{}, 1),
	}

	go func() {
		for {
			conn, err := net.Dial((configuratoraddr)[:strings.Index(configuratoraddr, ":")], (configuratoraddr)[strings.Index(configuratoraddr, ":")+1:])
			if err != nil {
				l.Error("Dial", err)
				goto timeout
			}

			if err = c.handshake(conn); err != nil {
				conn.Close()
				l.Error("handshake", err)
				goto timeout
			}
			if c.conn, err = connector.NewEpollReConnector[connector.BasicMessage](conn, c, c.handshake, c.afterConnProc); err != nil {
				l.Error("NewEpollReConnector", err)
				goto timeout
			}
			if err = c.conn.StartServing(); err != nil {
				c.conn.ClearFromCache()
				l.Error("StartServing", err)
				goto timeout
			}
			if err = c.afterConnProc(); err != nil {
				c.conn.Close(err)
				l.Error("afterConnProc", err)
				goto timeout
			}
			break
		timeout:
			l.Debug("First connection", "failed, timeout")
			time.Sleep(time.Second * 2)
		}
	}()

	return c
}

func (c *configurator) handshake(conn net.Conn) error {
	if _, err := conn.Write(connector.FormatBasicMessage([]byte(c.thisServiceName))); err != nil {
		return err
	}
	buf := make([]byte, 5)
	conn.SetReadDeadline(time.Now().Add(time.Second * 2))
	n, err := conn.Read(buf)
	if err != nil {
		return errors.New(suckutils.ConcatTwo("err reading configurator's approving, err: ", err.Error()))
	}
	if n == 5 {
		if buf[4] == byte(configuratortypes.OperationCodeOK) {
			return nil
		} else if buf[4] == byte(configuratortypes.OperationCodeNOTOK) {
			if c.conn != nil {
				go c.conn.CancelReconnect() // горутина пушто этот хэндшейк под залоченным мьютексом выполняется
			}
			c.terminationByConfigurator <- struct{}{}
			return errors.New("configurator do not approve this service")
		}
	}
	return errors.New("configurator's approving format not supported or weird")
}

func (c *configurator) afterConnProc() error {

	myStatus := byte(configuratortypes.StatusSuspended)
	if c.servStatus.onAir() {
		myStatus = byte(configuratortypes.StatusOn)
	}
	if err := c.conn.Send(connector.FormatBasicMessage([]byte{byte(configuratortypes.OperationCodeMyStatusChanged), myStatus})); err != nil {
		return err
	}

	if c.publishers != nil {
		pubnames := c.publishers.GetAllPubNames()
		if len(pubnames) != 0 {
			message := append(make([]byte, 0, len(pubnames)*15), byte(configuratortypes.OperationCodeSubscribeToServices))
			for _, pub_name := range pubnames {
				pub_name_byte := []byte(pub_name)
				message = append(append(message, byte(len(pub_name_byte))), pub_name_byte...)
			}
			if err := c.conn.Send(connector.FormatBasicMessage(message)); err != nil {
				return err
			}
		}
	}

	if err := c.conn.Send(connector.FormatBasicMessage([]byte{byte(configuratortypes.OperationCodeGiveMeOuterAddr)})); err != nil {
		return err
	}
	return nil
}

func (c *configurator) send(message []byte) error {
	if c == nil {
		return errors.New("nil configurator")
	}
	if c.conn == nil {
		return connector.ErrNilConn
	}
	if c.conn.IsClosed() {
		return connector.ErrClosedConnector
	}
	if err := c.conn.Send(message); err != nil {
		c.conn.Close(err)
		return err
	}
	return nil
}

func (c *configurator) onSuspend(reason string) {
	c.l.Warning("OwnStatus", suckutils.ConcatTwo("suspended, reason: ", reason))
	c.send(connector.FormatBasicMessage([]byte{byte(configuratortypes.OperationCodeMyStatusChanged), byte(configuratortypes.StatusSuspended)}))
}

func (c *configurator) onUnSuspend() {
	c.l.Warning("OwnStatus", "unsuspended")
	c.send(connector.FormatBasicMessage([]byte{byte(configuratortypes.OperationCodeMyStatusChanged), byte(configuratortypes.StatusOn)}))
}

func (c *configurator) Handle(message *connector.BasicMessage) error {
	if len(message.Payload) == 0 {
		return connector.ErrEmptyPayload
	}
	switch configuratortypes.OperationCode(message.Payload[0]) {
	case configuratortypes.OperationCodePing:
		return nil
	case configuratortypes.OperationCodeMyStatusChanged:
		return nil
	case configuratortypes.OperationCodeImSupended:
		return nil
	case configuratortypes.OperationCodeSetOutsideAddr:
		if len(message.Payload) < 2 {
			return connector.ErrWeirdData
		}
		if len(message.Payload) < 2+int(message.Payload[1]) {
			return connector.ErrWeirdData
		}
		if netw, addr, err := configuratortypes.UnformatAddress(message.Payload[2 : 2+int(message.Payload[1])]); err != nil {
			return err
		} else {
			if netw == netprotocol.NetProtocolNil {
				c.listener.stop()
				c.servStatus.setListenerStatus(true)
				return nil
			}
			if cur_netw, cur_addr := c.listener.Addr(); cur_addr == addr && cur_netw == netw.String() {
				return nil
			}
			var err error
			for i := 0; i < 3; i++ {
				if err = c.listener.listen(netw.String(), addr); err != nil {
					c.listener.l.Error("listen", err)
					time.Sleep(time.Second)
				} else {
					return nil
				}
			}
			return err
		}
	case configuratortypes.OperationCodeUpdatePubs:
		updates := configuratortypes.SeparatePayload(message.Payload[1:])
		if len(updates) != 0 {
			for _, update := range updates {
				pubname, raw_addr, status, err := configuratortypes.UnformatOpcodeUpdatePubMessage(update)
				if err != nil {
					return err
				}
				if c.publishers == nil {
					c.l.Error("Handle/OperationCodeUpdatePubs", errors.New(suckutils.ConcatThree("service have no pubs, but recieved update for pub pub: \"", string(pubname), "\", sending unsubscription")))
					message := append(append(make([]byte, 0, 2+len(pubname)), byte(configuratortypes.OperationCodeUnsubscribeFromServices), byte(len(pubname))), pubname...)
					if err := c.send(connector.FormatBasicMessage(message)); err != nil {
						c.l.Error("Handle/configurator.send", err)
					}
					continue
				}
				netw, addr, err := configuratortypes.UnformatAddress(raw_addr)
				if err != nil {
					c.l.Error("Handle/OperationCodeUpdatePubs/UnformatAddress", err)
					return connector.ErrWeirdData
				}
				if netw == netprotocol.NetProtocolNonlocalUnix {
					continue // TODO:
				}

				c.publishers.update(ServiceName(pubname), netw.String(), addr, status)
			}
			return nil
		} else {
			return connector.ErrWeirdData
		}
	}
	return connector.ErrWeirdData
}

func (c *configurator) HandleClose(reason error) {
	c.l.Warning("Configurator", suckutils.ConcatTwo("conn closed, reason err: ", reason.Error()))
	// в суспенд не уходим, пока у нас есть паблишеры - нам пофиг
}
