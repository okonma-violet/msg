package protocol

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/gobwas/ws"
)

type AppServerMessage struct {
	Type MessageType // 1 byte
	// reserved1   byte          // 1 byte
	ApplicationID  AppID
	ConnectionUID  ConnUID
	Generation     byte
	RawMessageData []byte // whole headers and body in unparsed, virgin form, with their lengths specified
	Timestamp      int64
}

const AppServer_MaxConnUID = 16777215

// FOR CLIENTS CONNS ONLY
func (m *AppServerMessage) ReadWS(r io.Reader, h ws.Header) error {
	if h.Length < Client_message_head_len {
		return errors.New("ws payload is less than client message head len")
	}
	payload := make([]byte, h.Length)
	_, err := io.ReadFull(r, payload)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(payload[12:14])
	body_len := byteOrder.Uint32(payload[14:18])
	if h.Length != int64(Client_message_head_len)+int64(headers_len)+int64(body_len) {
		return errors.New("wsheader.length does not match with specified len in protocol's message head")
	}

	m.Type = MessageType(payload[0])
	m.ApplicationID = AppID(byteOrder.Uint16(payload[2:4]))
	m.Timestamp = int64(byteOrder.Uint64(payload[4:12])) // нужно ли читать таймстемп присланный клиентом???
	m.RawMessageData = payload[12:]

	return nil
}

// FOR APPS CONNS ONLY
func (m *AppServerMessage) Read(conn net.Conn) error {

	head := make([]byte, App_message_head_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(head[14:16])
	body_len := byteOrder.Uint32(head[16:20])

	m.RawMessageData = make([]byte, uint32(headers_len)+body_len+6)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err = io.ReadFull(conn, m.RawMessageData[6:])
	if err != nil {
		return err
	}
	copy(m.RawMessageData[0:2], head[14:16]) // headers len
	copy(m.RawMessageData[2:6], head[16:20]) // body len

	m.Type = MessageType(head[0])
	m.ConnectionUID = ConnUID(byteOrder.Uint32(head[2:6]) & 16777215)
	m.Generation = head[2]
	m.Timestamp = int64(byteOrder.Uint64(head[6:14]))

	return nil
}

func (m *AppServerMessage) ReadWithoutDeadline(conn net.Conn) error {

	head := make([]byte, App_message_head_len)
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(head[14:16])
	body_len := byteOrder.Uint32(head[16:20])

	m.RawMessageData = make([]byte, uint32(headers_len)+body_len+6)
	_, err = io.ReadFull(conn, m.RawMessageData[6:])
	if err != nil {
		return err
	}
	copy(m.RawMessageData[0:2], head[14:16]) // headers len
	copy(m.RawMessageData[2:6], head[16:20]) // body len

	m.Type = MessageType(head[0])
	m.ConnectionUID = ConnUID(byteOrder.Uint32(head[2:6]) & 16777215)
	m.Generation = head[2]
	m.Timestamp = int64(byteOrder.Uint64(head[6:14]))

	return nil
}

func DecodeClientMessageToAppServerMessage(rawmessage []byte) (*AppServerMessage, error) {
	if len(rawmessage) < Client_message_head_len {
		return nil, errors.New("weird data(raw message does not satisfy min head len)")
	}
	headers_len := byteOrder.Uint16(rawmessage[12:14])
	body_len := byteOrder.Uint32(rawmessage[14:18])
	if len(rawmessage) != Client_message_head_len+int(headers_len)+int(body_len) {
		println(Client_message_head_len, headers_len, body_len)
		return nil, errors.New("weird data(raw message is shorter/longer than specified head, body and headers lengths)")
	}

	return &AppServerMessage{
		Type:           MessageType(rawmessage[0]),
		ApplicationID:  AppID(byteOrder.Uint16(rawmessage[2:4])),
		Timestamp:      int64(byteOrder.Uint64(rawmessage[4:12])), // нужно ли читать таймстемп присланный клиентом???
		RawMessageData: rawmessage[12:],
	}, nil
}

func DecodeAppMessageToAppServerMessage(rawmessage []byte) (*AppServerMessage, error) {
	if len(rawmessage) < App_message_head_len {
		return nil, errors.New("weird data(raw message does not satisfy min head len)")
	}
	headers_len := byteOrder.Uint16(rawmessage[14:16])
	body_len := byteOrder.Uint32(rawmessage[16:20])
	if len(rawmessage) != App_message_head_len+int(headers_len)+int(body_len) {
		return nil, errors.New("weird data(raw message is shorter/longer than specified head, body and headers lengths)")
	}

	return &AppServerMessage{
		Type:           MessageType(rawmessage[0]),
		Generation:     rawmessage[2],
		ConnectionUID:  ConnUID(byteOrder.Uint32(rawmessage[2:6]) & 16777215), // 16777215 = {0,255,255,255}
		Timestamp:      int64(byteOrder.Uint64(rawmessage[6:14])),
		RawMessageData: rawmessage[6:],
	}, nil
}

func (m *AppServerMessage) EncodeToClientMessage() []byte {
	encoded := make([]byte, Client_message_head_len-6+len(m.RawMessageData))

	encoded[0] = byte(m.Type)

	byteOrder.PutUint16(encoded[2:4], uint16(m.ApplicationID))
	byteOrder.PutUint64(encoded[4:12], uint64(m.Timestamp))

	copy(encoded[12:], m.RawMessageData)

	return encoded
}

func (m *AppServerMessage) EncodeToAppMessage() ([]byte, error) {
	if m.ConnectionUID > AppServer_MaxConnUID {
		return nil, errors.New("connuid must be less then 16777215")
	}
	encoded := make([]byte, App_message_head_len-6+len(m.RawMessageData))

	encoded[0] = byte(m.Type)

	byteOrder.PutUint32(encoded[2:6], uint32(m.ConnectionUID))
	byteOrder.PutUint64(encoded[6:14], uint64(m.Timestamp))
	copy(encoded[14:], m.RawMessageData)

	encoded[2] = m.Generation

	return encoded, nil
}
