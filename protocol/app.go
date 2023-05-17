package protocol

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/gobwas/ws"
)

type AppMessage struct {
	Type MessageType // 1 byte
	// reserved1 // 1 byte
	ConnectionUID ConnUID
	Headers       []byte // uint16 len
	Body          []byte // uint32 len
	Timestamp     int64
}

const App_message_head_len = 20

func (m *AppMessage) ReadWS(r io.Reader, h ws.Header) error {
	if h.Length < App_message_head_len {
		return errors.New("ws payload is less than app message head len")
	}
	payload := make([]byte, h.Length)
	_, err := io.ReadFull(r, payload)
	if err != nil {
		return err
	}
	headers_len := byteOrder.Uint16(payload[14:16])
	body_len := byteOrder.Uint32(payload[16:20])
	if h.Length != int64(App_message_head_len)+int64(headers_len)+int64(body_len) {
		return errors.New("wsheader.length does not match with specified len in protocol's message head")
	}
	m.Headers = payload[20 : 20+headers_len]
	m.Body = payload[20+headers_len:]

	m.Type = MessageType(payload[0])
	m.ConnectionUID = ConnUID(byteOrder.Uint32(payload[2:6]))
	m.Timestamp = int64(byteOrder.Uint64(payload[6:14]))
	return nil

}

func (m *AppMessage) Read(conn net.Conn) error {

	head := make([]byte, App_message_head_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(head[14:16])
	body_len := byteOrder.Uint32(head[16:20])

	m.Headers = make([]byte, headers_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err = io.ReadFull(conn, m.Headers)
	if err != nil {
		return err
	}

	m.Body = make([]byte, body_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err = io.ReadFull(conn, m.Body)
	if err != nil {
		return err
	}

	m.Type = MessageType(head[0])
	m.ConnectionUID = ConnUID(byteOrder.Uint32(head[2:6]))
	m.Timestamp = int64(byteOrder.Uint64(head[6:14]))

	return nil
}

func (m *AppMessage) ReadWithoutDeadline(conn net.Conn) error {

	head := make([]byte, App_message_head_len)
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(head[14:16])
	body_len := byteOrder.Uint32(head[16:20])

	m.Headers = make([]byte, headers_len)
	_, err = io.ReadFull(conn, m.Headers)
	if err != nil {
		return err
	}

	m.Body = make([]byte, body_len)
	_, err = io.ReadFull(conn, m.Body)
	if err != nil {
		return err
	}

	m.Type = MessageType(head[0])
	m.ConnectionUID = ConnUID(byteOrder.Uint32(head[2:6]))
	m.Timestamp = int64(byteOrder.Uint64(head[6:14]))

	return nil
}

func (m *AppMessage) Byte() ([]byte, error) {
	return EncodeAppMessage(m.Type, m.ConnectionUID, m.Timestamp, m.Headers, m.Body)
}

// do not knows about generation in connUID
func EncodeAppMessage(messagetype MessageType, connUID ConnUID, timestamp int64, headers []byte, body []byte) ([]byte, error) {
	// if connUID == 0 {
	// 	return nil, errors.New("connUID is zero")
	// }
	// if connUID > Max_ConnUID { // app не знает про генерейшн
	// 	return nil, errors.New("connuid must be less then 16777215")
	// }
	if len(headers) > 65535 {
		return nil, errors.New("headers overflows (len specified by uint16)")
	}
	if len(body) > 4294967295 {
		return nil, errors.New("body overflows (len specified by uint32)")
	}

	encoded := make([]byte, App_message_head_len+len(headers)+len(body))

	encoded[0] = byte(messagetype)

	byteOrder.PutUint32(encoded[2:6], uint32(connUID))
	byteOrder.PutUint64(encoded[6:14], uint64(timestamp))
	byteOrder.PutUint16(encoded[14:16], uint16(len(headers)))
	byteOrder.PutUint32(encoded[16:20], uint32(len(body)))

	copy(encoded[App_message_head_len:App_message_head_len+len(headers)], headers)
	copy(encoded[App_message_head_len+len(headers):], body)

	return encoded, nil
}

func DecodeAppMessage(rawmessage []byte) (*AppMessage, error) {
	if len(rawmessage) < App_message_head_len {
		return nil, errors.New("weird data(raw message does not satisfy min head len)")
	}
	headers_len := byteOrder.Uint16(rawmessage[14:16])
	body_len := byteOrder.Uint32(rawmessage[16:20])
	if len(rawmessage) != App_message_head_len+int(headers_len)+int(body_len) {
		return nil, errors.New("weird data(raw message is shorter/longer than specified head, body and headers lengths)")
	}

	return &AppMessage{
		Type:          MessageType(rawmessage[0]),
		ConnectionUID: ConnUID(byteOrder.Uint32(rawmessage[2:6])),
		Headers:       rawmessage[App_message_head_len : App_message_head_len+headers_len],
		Body:          rawmessage[App_message_head_len+headers_len:],
		Timestamp:     int64(byteOrder.Uint64(rawmessage[6:14])),
	}, nil
}

func (m *AppMessage) MessageID() int64 {
	return m.Timestamp
}
